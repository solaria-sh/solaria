# AGENTS.md

Guidance for AI coding agents working in this repository. People should start with [README.md](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md).

## Project

Solaria is a Go CLI that plans and applies agent definitions across hosts, Terraform-style. It is a control plane: it never runs an agent loop itself (see [ADR 0002](docs/adr/0002-control-plane-not-agent-framework.md)). Read the ADRs in `docs/adr/` before changing anything structural. They are the source of truth for the design.

## Commands

```bash
make build     # bin/solaria
make test      # go test -race ./...
make lint      # golangci-lint
make check     # everything CI runs; run this before you finish
```

To update golden files after an intended output change, run `go test ./... -update` and review the diff.

## Layout

- `cmd/solaria/`: the entry point only. No logic here.
- `internal/cli/`: cobra commands. Keep them thin; they call into the engine.
- `internal/api/v1alpha1/`: resource types (Agent, Environment, McpServer). These are the source of truth for the JSON Schema in `schemas/`.
- `internal/provider/`: the provider interface. `internal/providers/<name>/` holds the implementations.
- `internal/engine/`, `internal/state/`, `internal/resolve/`, `internal/digest/`: the core.
- `docs/adr/`: architecture decisions. `docs/spikes/`: research notes that feed them.

Everything is under `internal/` until the API stabilizes. Don't add public packages without an ADR.

## Conventions

- Standard Go style; `gofumpt` and `goimports` formatting, enforced by the linter.
- Wrap errors with context (`fmt.Errorf("load %s: %w", path, err)`). Never panic on user input.
- User-facing errors name the file and field so the problem can be fixed.
- Unit tests must not use the network, Docker or real credentials. Use fakes (`httptest`, interface fakes). Integration tests are opt-in through `SOLARIA_IT_*` environment variables.
- Secrets never reach state, digests, logs, plan output or rendered files. Only the names of secret references do.
- Conventional Commits, signed off (`git commit -s`).
- A change to an architectural decision needs an updated or new ADR in the same pull request.
