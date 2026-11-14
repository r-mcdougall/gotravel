.DEFAULT_GOAL := help

COMPOSE := docker compose
JWT_PRIVATE_KEY_FILE := deploy/keys/jwt_private.pem

.PHONY: help up down reset logs ps psql lint fmt test sqlc keys seed

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-8s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.env:
	cp .env.example .env
	@echo "Created .env from .env.example; review it before continuing."

up: .env keys ## Start all services in the background
	$(COMPOSE) up -d --build --wait

down: ## Stop all services
	$(COMPOSE) down

reset: ## Stop all services and DELETE all volumes
	$(COMPOSE) down --volumes

logs: ## Follow logs of all services
	$(COMPOSE) logs -f

ps: ## Show service status
	$(COMPOSE) ps

psql: ## Open psql as the superuser
	$(COMPOSE) exec postgres sh -c 'psql -U "$$POSTGRES_USER" -d postgres'

lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format code (gofmt + goimports)
	golangci-lint fmt ./...

test: ## Run tests with the race detector
	go test -race ./...

sqlc: ## Generate Go code from SQL queries
	@for f in services/*/sqlc.yaml; do (cd $$(dirname $$f) && go tool sqlc generate) || exit 1; done

keys: ## Generate JWT signing keys in deploy/keys
	@test -f $(JWT_PRIVATE_KEY_FILE) || go run ./tools/keygen

seed: .env ## Seed the catalog with flights through the gateway
	@set -a && . ./.env && set +a && go run ./tools/seed
