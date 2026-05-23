DB_CONN ?= postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable
MIGRATIONS_DIR = internal/db/migrations
BINARY = radarul-api

.PHONY: build run db-up db-down migrate-up migrate-down sqlc-gen seed test lint tunnel demo-spray gen-vapid

build:
	go build -o $(BINARY) ./cmd/server

run:
	go run ./cmd/server

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

migrate-up:
	goose -dir $(MIGRATIONS_DIR) postgres "$(DB_CONN)" up

migrate-down:
	goose -dir $(MIGRATIONS_DIR) postgres "$(DB_CONN)" down

migrate-new:
	@if [ -z "$(NAME)" ]; then echo "Usage: make migrate-new NAME=description"; exit 1; fi
	goose -dir $(MIGRATIONS_DIR) create $(NAME) sql

sqlc-gen:
	sqlc generate

seed:
	go run ./cmd/server --seed

test:
	go test ./... -v -count=1

lint:
	go vet ./...

gen-vapid:
	go run ./tools/gen-vapid/main.go

tunnel:
	@echo "Starting cloudflared tunnel to http://localhost:9090 ..."
	@echo "Copy the HTTPS URL printed below and set APP_BASE_URL in your .env"
	cloudflared tunnel --url http://localhost:9090

demo-spray:
	@bash scripts/demo-spray.sh
