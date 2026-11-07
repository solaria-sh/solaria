GOLANGCI_LINT_VERSION ?= v2.15.0
GOVULNCHECK_VERSION   ?= v1.9.0
GORELEASER_VERSION    ?= v2.20.0

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --verify --quiet HEAD)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

PKG     := github.com/solaria-sh/solaria
LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK   := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
GORELEASER    ?= go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

.PHONY: all build test lint fmt vuln tidy-check schema check-schema check release-check release-snapshot clean

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

schema: ## Regenerate schemas/ and docs/reference.md from internal/api
	go run ./internal/schemagen/cmd/schemagen

check-schema: schema ## Fail if schemas/ is out of date
	git diff --exit-code -- schemas/ docs/reference.md

check: tidy-check lint test vuln ## Everything CI runs (tests include the schema drift check)

release-check: ## Validate .goreleaser.yaml
	$(GORELEASER) check

release-snapshot: ## Build a local release into dist/ without signing or publishing
	$(GORELEASER) release --snapshot --clean --skip=sign

clean:
	rm -rf bin dist
