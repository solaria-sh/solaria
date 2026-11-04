# AGENTS.md

Guidance for AI coding agents working in this repository. People should start with [README.md](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md).

## Project

Solaria is a Go CLI that plans and applies agent definitions across hosts, Terraform-style. It is a control plane: it never runs an agent loop itself (see [ADR 0002](docs/adr/0002-control-plane-not-agent-framework.md)). Read the ADRs in `docs/adr/` before changing anything structural. They are the source of truth for the design.

## Commands

```bash
make build     # bin/solaria
make test      # go test -race ./...
make lint      # golangci-lint
make schema    # regenerate schemas/ after changing internal/api
make check     # everything CI runs; run this before you finish
```

To update expected output after an intended change, run `go test ./... -update` and review the diff. CLI behavior is tested with [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) scripts in `internal/cli/testdata/script/`; add a script there for any new command or diagnostic.

## Layout

- `cmd/solaria/`: the entry point only. No logic here.
- `internal/cli/`: cobra commands. Keep them thin; they call into the engine.
- `internal/api/v1alpha1/`: resource types (Agent, Environment, McpServer). These are the source of truth for the JSON Schema in `schemas/`. Field doc comments become schema descriptions, so write them for people editing cards.
- `internal/schemagen/`: generates `schemas/v1alpha1/*.json` from the types. A test fails when the committed files are stale.
- `internal/config/`: finds YAML files, checks each document against its schema, and decodes it. Errors carry file, line and column.
- `internal/validate/`: checks across resources (references, files on disk, skills, portability warnings).
- `internal/diag/`: diagnostics shared by all of the above.
- `internal/resolve/`: applies environment overrides and references, reads instruction and skill files, and builds the `Definition` that providers render and digests hash.
- `internal/digest/`: RFC 8785 canonical JSON and SHA-256.
- `internal/provider/`: the provider interface, plans and field diffs. `internal/providers/<name>/` holds the implementations; `internal/providers/stub/` is the file-backed test provider used by examples and end-to-end tests.
- `internal/engine/`: refresh, plan, capability checks, apply and plan rendering. `internal/state/`: the state format and the local backend with its lock.
- `examples/`: runnable projects. `examples/hello` uses the stub provider and is copied into the end-to-end scripts.
- `docs/adr/`: architecture decisions. `docs/spikes/`: research notes that feed them.

Everything is under `internal/` until the API stabilizes. Don't add public packages without an ADR. The one exception is `schemas/`, which only embeds the published schema files (`go:embed` can't reach them from `internal/`).

## Conventions

- Standard Go style; `gofumpt` and `goimports` formatting, enforced by the linter.
- Wrap errors with context (`fmt.Errorf("load %s: %w", path, err)`). Never panic on user input.
- User-facing errors name the file and field so the problem can be fixed.
- Unit tests must not use the network, Docker or real credentials. Use fakes (`httptest`, interface fakes). Integration tests are opt-in through `SOLARIA_IT_*` environment variables.
- Secrets never reach state, digests, logs, plan output or rendered files. Only the names of secret references do.
- Anything that changes `resolve.Definition` or its normalization changes every digest. Bump `resolve.Format`, update ADR 0008, and only then regenerate the golden files.
- Conventional Commits, signed off (`git commit -s`).
- A change to an architectural decision needs an updated or new ADR in the same pull request.
