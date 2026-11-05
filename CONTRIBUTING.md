# Contributing to Solaria

Thanks for your interest. Solaria is in its design stage, so the most useful contributions right now are feedback on the [architecture decisions](docs/adr/) and issues describing hosts or agent setups you'd want supported.

## Development setup

You need Go 1.26 or newer (older `go` commands download the right toolchain automatically), `make`, and Docker for the container provider's integration tests.

```bash
git clone https://github.com/renantarouco/solaria.git
cd solaria
make test
```

| Command | What it does |
|---|---|
| `make build` | Builds `bin/solaria` |
| `make test` | Runs the unit tests with the race detector |
| `make lint` | Runs golangci-lint (installed on demand with `go run`) |
| `make vuln` | Runs govulncheck |
| `make schema` | Regenerates `schemas/` from the Go types in `internal/api` |
| `make check` | Everything CI runs |

After changing a resource type, run `make schema` and commit the result; the tests fail otherwise. After an intended change to CLI output, run `go test ./internal/cli -update` and review the diff.

Unit tests never touch the network or real hosts. Integration tests that do are opt-in through environment variables, documented next to each test:

```console
$ SOLARIA_IT_DOCKER=1 go test ./internal/providers/gitagent -run Integration -v -timeout 50m
$ SOLARIA_IT_MANAGED_AGENTS=1 ANTHROPIC_API_KEY=... go test ./internal/providers/managedagents -run Integration -v
```

The Managed Agents test creates real objects named `solaria-it-*` in the key's organization and archives the agent at the end, which can't be undone.

## Making changes

1. For anything beyond a small fix, open an issue first so we can agree on the approach.
2. Decisions that shape the architecture get an [ADR](docs/adr/). Propose one in your pull request, starting from [the template](docs/adr/template.md).
3. Keep pull requests focused, and add tests for behavior changes.
4. Run `make check` before pushing.

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(engine): show replaced resources in plan output
fix(gitagent): quote paths with spaces in the Dockerfile
docs(adr): record the state backend decision
```

Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `build`, `ci`, `chore`.

## Developer Certificate of Origin

Every commit must be signed off, certifying that you wrote the change or have the right to submit it under the project's license (see the [DCO](https://developercertificate.org/)):

```bash
git commit -s -m "feat(cli): add status command"
```

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
