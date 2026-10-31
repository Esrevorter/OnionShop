DATABASE_URL ?= postgres://onionshop:onionshop_dev_only@127.0.0.1:5432/onionshop?sslmode=disable

.PHONY: up down build run test vet lint migrate migrate-down sqlc clean

up:
	docker compose up -d postgres redis

down:
	docker compose down

build:
	go build -o bin/onionshop ./cmd/onionshop

run:
	go run ./cmd/onionshop

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

clean:
	rm -rf bin/
