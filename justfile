set dotenv-load

DB_CONN := env_var_or_default("DB_CONN_STR", "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable")
MIGRATIONS_DIR := "internal/db/migrations"
BINARY := "radarul-api"

build:
	go build -o {{BINARY}} ./cmd/server

run:
	go run ./cmd/server

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

migrate-up:
	goose -dir {{MIGRATIONS_DIR}} postgres "{{DB_CONN}}" up

migrate-down:
	goose -dir {{MIGRATIONS_DIR}} postgres "{{DB_CONN}}" down

migrate-new name:
	goose -dir {{MIGRATIONS_DIR}} create {{name}} sql

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
	@echo "Copy the HTTPS URL below into APP_BASE_URL in .env"
	cloudflared tunnel --url http://localhost:8080

demo-spray:
	bash scripts/demo-spray.sh
