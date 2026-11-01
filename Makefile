DATABASE_URL ?= postgres://onionshop:onionshop_dev_only@127.0.0.1:5432/onionshop?sslmode=disable

.PHONY: up down build run test vet lint migrate migrate-down sqlc clean certs check install compose-config panic-wipe-dry

up: ## start full stack (requires .env; see INSTALL.md)
	docker compose up -d

down: ## stop the stack (keeps volumes)
	docker compose down

compose-config: ## validate docker-compose.yml after edits
	docker compose config >/dev/null && echo OK

build: ## build the Go binary locally
	go build -o bin/onionshop ./cmd/onionshop

run: ## run locally against loopback TCP (dev only)
	ONIONSHOP_SECURE_COOKIES=false go run ./cmd/onionshop -addr 127.0.0.1:8080

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	staticcheck ./...
	golangci-lint run

migrate:
	migrate -path db/migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path db/migrations -database "$(DATABASE_URL)" down 1

sqlc:
	sqlc generate

certs: ## generate nginx self-signed cert + print SPKI pin to record (scripts/gen-certs.sh)
	./scripts/gen-certs.sh

check: ## installer dry-run: verify host dependencies only
	./install/install.sh --check-only

install: ## guided one-shot deployment for less-experienced operators (INSTALL.md)
	sudo ./install/install.sh

panic-wipe-dry: ## show what scripts/panic-wipe.sh would do (no execution)
	echo "READ scripts/panic-wipe.sh header first. Real invocation requires:"
	echo "  sudo ./scripts/panic-wipe.sh --i-understand-this-is-irreversible --host=\$\(hostname\)"

clean:
	rm -rf bin/
