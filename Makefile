.DEFAULT_GOAL := help

# Dev engine environment (README.md "Running the engine"). Override any of
# these on the command line, e.g. `make engine GOERP_ADMIN_TOKEN=other`.
GOERP_DB_PRIMARY_DSN ?= postgres://engine_user:dev@localhost:6432/goerp_dev
GOERP_DB_SCHEMA_SYNC_DSN ?= postgres://schema_sync_user:dev@localhost:55432/goerp_dev
GOERP_ADMIN_TOKEN ?= dev-admin-token
GOERP_STORAGE_LOCAL_DIR ?= $(CURDIR)/storage
GOERP_MODULE_DIR ?= $(CURDIR)/.dev/modules
GOERP_PLATFORM_DOMAIN ?= localhost
GOERP_REGISTRATION_ENABLED ?= true
# The shell's Vite dev server (make shell), so emailed links open there.
GOERP_APP_BASE_URL ?= http://localhost:5173

# The package lists of the CLI and engine jobs in .github/workflows/go.yml,
# which run these targets with CI's own GO_TEST_FLAGS.
CLI_PKGS := ./internal/cli/... ./internal/gateway/... ./internal/module/... ./cmd/goerp/... ./cmd/admin-gateway/...
ENGINE_PKGS := ./internal/engine/... ./internal/module/... ./sdk/... ./contract/... ./cmd/engine/...
GO_TEST_FLAGS ?= -race -timeout 30m

.PHONY: help infra infra-down engine module shell storybook check-go test-cli test-engine check-shell

help: ## List the targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

infra: ## Start the dev infrastructure (compose.dev.yml) and wait until healthy
	docker compose -f compose.dev.yml up -d --wait

infra-down: ## Stop the dev infrastructure, keeping its volumes
	docker compose -f compose.dev.yml down

engine: ## Run the engine against the dev infrastructure, loading modules from GOERP_MODULE_DIR
	mkdir -p $(GOERP_STORAGE_LOCAL_DIR) $(GOERP_MODULE_DIR)
	GOERP_DB_PRIMARY_DSN='$(GOERP_DB_PRIMARY_DSN)' \
	GOERP_DB_SCHEMA_SYNC_DSN='$(GOERP_DB_SCHEMA_SYNC_DSN)' \
	GOERP_ADMIN_TOKEN='$(GOERP_ADMIN_TOKEN)' \
	GOERP_STORAGE_LOCAL_DIR='$(GOERP_STORAGE_LOCAL_DIR)' \
	GOERP_MODULE_DIR='$(GOERP_MODULE_DIR)' \
	GOERP_PLATFORM_DOMAIN='$(GOERP_PLATFORM_DOMAIN)' \
	GOERP_REGISTRATION_ENABLED='$(GOERP_REGISTRATION_ENABLED)' \
	GOERP_APP_BASE_URL='$(GOERP_APP_BASE_URL)' \
	go run ./cmd/engine

module: ## Build module M (e.g. M=modules/demo) into GOERP_MODULE_DIR; restart the engine to load it
	@test -n "$(M)" || { echo "usage: make module M=<module dir>"; exit 2; }
	mkdir -p $(GOERP_MODULE_DIR)
	go run ./cmd/goerp module build $(M) --output $(GOERP_MODULE_DIR)/$(notdir $(abspath $(M))).erp

shell: ## Run the shell app's Vite dev server
	cd shell && npm run dev

storybook: ## Run Storybook
	cd shell && npm run storybook

check-go: ## Build, vet, gofmt and golangci-lint the Go code
	go build ./...
	go vet ./...
	@out="$$(git ls-files -z --cached --others --exclude-standard '*.go' | xargs -0 "$$(go env GOROOT)/bin/gofmt" -l)" || exit 1; \
		if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	golangci-lint run ./...

test-cli: ## Run the CI CLI job's Go tests (needs `make infra`)
	go test $(GO_TEST_FLAGS) $(CLI_PKGS)

test-engine: ## Run the CI engine job's Go tests (needs `make infra`)
	go test $(GO_TEST_FLAGS) $(ENGINE_PKGS)

check-shell: ## Format, lint, typecheck and test the shell workspace
	cd shell && npm run format && npm run lint && npm run typecheck && npm run test
