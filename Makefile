.DEFAULT_GOAL := help

BIN     := bin/envoyadmin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

GOLANGCI_LINT_VERSION ?= v2.14.0
GOLANGCI_LINT         := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: help build run test test-update lint fmt tidy cover clean check

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

build: ## Build bin/envoyadmin
	go build $(LDFLAGS) -o $(BIN) ./cmd/envoyadmin

run: build ## Build and run against the built-in demo server
	$(BIN) --demo

test: ## Run all tests with the race detector
	go test -race ./...

test-update: ## Regenerate teatest golden files
	go test ./internal/ui/... -update

lint:
	$(GOLANGCI_LINT) run --fix ./...

fmt: ## Format the code
	gofmt -s -w .

tidy: ## go mod tidy
	go mod tidy

cover: ## Coverage summary
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

clean: ## Remove build and coverage output
	rm -rf bin coverage.out

check: lint test
