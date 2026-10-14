# Run `make` or `make help` to see every command.
.DEFAULT_GOAL := help
SHELL := /bin/bash

-include .env
export

BIN := bin/api

## ---------- Go API ----------

run: ## Start the API locally (http://localhost:8080/health)
	go run ./cmd/api

build: ## Compile the API into bin/api
	CGO_ENABLED=0 go build -o $(BIN) ./cmd/api

test: ## Run all Go tests
	go test -race ./...

fmt: ## Format all Go code
	gofmt -w .

check: ## Run the same checks as CI (format, vet, tests)
	@test -z "$$(gofmt -l .)" || (echo "Run 'make fmt' — these files are not formatted:"; gofmt -l .; exit 1)
	go vet ./...
	go test -race ./...

## ---------- FreeRADIUS (WSL / Ubuntu) ----------

radius-setup: ## First-time setup: install + database + config + tests
	./scripts/wsl/01-install.sh
	./scripts/wsl/02-setup-database.sh
	./scripts/wsl/03-configure-freeradius.sh
	./scripts/wsl/04-test-radius.sh

radius-config: ## Re-apply radius/*.tmpl after editing them or .env
	./scripts/wsl/03-configure-freeradius.sh

radius-test: ## Check login, rejection and accounting all work
	./scripts/wsl/04-test-radius.sh

radius-start: ## Start PostgreSQL and FreeRADIUS (needed after every WSL restart)
	sudo service postgresql start
	sudo service freeradius start

radius-stop: ## Stop FreeRADIUS
	sudo service freeradius stop

radius-status: ## Is FreeRADIUS running?
	sudo service freeradius status

radius-debug: ## Run FreeRADIUS in the foreground, printing every request (Ctrl+C to stop)
	-sudo service freeradius stop
	sudo freeradius -X

db-shell: ## Open a SQL prompt on the radius database
	PGPASSWORD="$(RADIUS_DB_PASSWORD)" psql -h $(RADIUS_DB_HOST) -U $(RADIUS_DB_USER) -d $(RADIUS_DB_NAME)

## ---------- Help ----------

help: ## Show this list
	@awk 'BEGIN{FS=":.*## "} /^## /{printf "\n\033[1m%s\033[0m\n", substr($$0,4)} /^[a-z-]+:.*## /{printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: run build test fmt check radius-setup radius-config radius-test radius-start radius-stop radius-status radius-debug db-shell help
