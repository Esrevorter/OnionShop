.PHONY: up down build run test vet lint migrate migrate-down sqlc clean certs check install compose-config panic-wipe-dry

up: ## start full stack (requires .env; see INSTALL.md)
	docker compose up -d

down: ## stop the stack (keeps volumes)
	docker compose down

compose-config: ## validate docker-compose.yml after edits
	docker compose config >/dev/null && echo OK

build: sqlc ## build the Go binary locally
	go build -o bin/onionshop ./cmd/onionshop

run: ## run locally against loopback TCP (dev only)
	ONIONSHOP_SECURE_COOKIES=false go run ./cmd/onionshop -addr 127.0.0.1:8080

test: sqlc
	go test ./...

vet:
	go vet ./...

# lint pins both tools to the exact versions used in CI (.github/workflows/ci.yml)
# via `go run ...@version`, so no local installation is required and PATH drift
# is impossible. GOTOOLCHAIN=local prevents silent toolchain switching.
#
# staticcheck: honnef.co/go/tools v0.6.1 matches what golangci-lint v1.64.8
# bundles internally. Latest staticcheck (>= v0.7.0) requires Go >= 1.26, which
# exceeds the project pin of Go 1.25.12.
#
# golangci-lint: v1.64.8 is the exact version pinned in CI. Running it via
# `go run` ensures we always get the same binary regardless of what is
# installed in /usr/local/bin or ~/go/bin.
lint: sqlc vet
	GOTOOLCHAIN=local go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...
	GOTOOLCHAIN=local go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run --timeout 5m ./...

# migrate requires DATABASE_URL to be set in the environment. This prevents
# silent authentication failures when the Makefile's default credential doesn't
# match the randomly-generated password in .env. Source .env first:
#   set -a; . ./.env; set +a
#   make migrate
migrate:
	@if [ -z "$$DATABASE_URL" ]; then \
		echo "ERROR: DATABASE_URL not set" >&2; \
		echo "Source your .env file first:" >&2; \
		echo "  set -a; . ./.env; set +a" >&2; \
		echo "  make migrate" >&2; \
		exit 1; \
	fi
	migrate -path db/migrations -database "$$DATABASE_URL" up

migrate-down:
	@if [ -z "$$DATABASE_URL" ]; then \
		echo "ERROR: DATABASE_URL not set" >&2; \
		echo "Source your .env file first:" >&2; \
		echo "  set -a; . ./.env; set +a" >&2; \
		echo "  make migrate-down" >&2; \
		exit 1; \
	fi
	migrate -path db/migrations -database "$$DATABASE_URL" down 1

sqlc: ## generate sqlc bindings from db/schema.sql and db/query/**
	sqlc generate

certs: ## Generate self-signed ECDSA P-384 TLS cert with exact onion DNS SAN; NO SPKI pin
	@test -n "$(ONION_HOST)" || (echo "Usage: make certs ONION_HOST=<v3-onion-hostname>.onion" >&2; exit 2)
	./scripts/gen-certs.sh "$(ONION_HOST)"

check: ## installer dry-run: verify host dependencies only
	./install/install.sh --check-only

install: ## guided preview deployment; see TLS/production gates in INSTALL.md
	sudo ./install/install.sh

panic-wipe-dry: ## show what scripts/panic-wipe.sh would do (no execution)
	echo "READ scripts/panic-wipe.sh header first. Real invocation requires:"
	echo "  sudo ./scripts/panic-wipe.sh --i-understand-this-is-irreversible --host=\$\(hostname\)"

clean:
	rm -rf bin/

.PHONY: tplcheck
tplcheck: ## Parse every template with html/template
	go run ./cmd/tplcheck
