.DEFAULT_GOAL := help

MISE ?= mise
ARGS ?=

.PHONY: help build test test-race run install format format-check vet check

help: ## Show available targets
	@printf "Available targets:\n"
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build all Go packages
	@$(MISE) run build

test: ## Run the test suite
	@$(MISE) run test

test-race: ## Run tests with the race detector
	@$(MISE) run test-race

run: ## Run Mesij; pass CLI arguments with ARGS="..."
	@$(MISE) run run -- $(if $(strip $(ARGS)),$(ARGS),help)

install: ## Install the Mesij CLI
	@$(MISE) run install

format: ## Format Go source files
	@$(MISE) run format

format-check: ## Check Go source formatting
	@$(MISE) run format-check

vet: ## Run Go static analysis
	@$(MISE) run vet

check: ## Run all non-destructive verification
	@$(MISE) run check
