.DEFAULT_GOAL := help

COMPOSE := docker compose

.PHONY: help up down reset logs ps psql lint fmt test

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-8s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.env:
	cp .env.example .env
	@echo "Created .env from .env.example; review it before continuing."

up: .env ## Start all services in the background
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
