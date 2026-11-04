GOLANGCI_LINT_VERSION ?= v2.15.0
GOVULNCHECK_VERSION   ?= v1.9.0

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --verify --quiet HEAD)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

PKG     := github.com/renantarouco/solaria
LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK   := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

.PHONY: all build test lint fmt vuln tidy-check check clean

all: check build

build: ## Build bin/solaria
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/solaria ./cmd/solaria

test: ## Run unit tests with the race detector
	go test -race -count=1 ./...

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run

fmt: ## Format code with the linter's formatters
	$(GOLANGCI_LINT) fmt

vuln: ## Check dependencies for known vulnerabilities
	$(GOVULNCHECK) ./...

tidy-check: ## Fail if go.mod or go.sum need tidying
	go mod tidy -diff

check: tidy-check lint test vuln ## Everything CI runs

clean:
	rm -rf bin dist
