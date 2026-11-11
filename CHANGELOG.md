# Changelog

All notable changes to this project are documented here. The project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

After 0.1.0, [release-please](https://github.com/googleapis/release-please) writes this file from the Conventional Commit messages on `main`: `feat` under Added, `fix` under Fixed, `perf` and `revert` under Changed, breaking changes first. Don't edit it by hand; see [Releasing](CONTRIBUTING.md#releasing) for how to change an entry. 0.1.0 was written by hand in the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format.

## [0.10.0](https://github.com/solaria-sh/solaria/compare/v0.9.0...v0.10.0) (2026-11-11)


### Added

* **vault:** create host vaults on Managed Agents ([#31](https://github.com/solaria-sh/solaria/issues/31)) ([01371f4](https://github.com/solaria-sh/solaria/commit/01371f452ef3ef2efd4edc6bf5726ffb5841b8d8))

## [0.9.0](https://github.com/solaria-sh/solaria/compare/v0.8.0...v0.9.0) (2026-11-11)


### Added

* **agent:** add spec.output.schema for structured answers ([#28](https://github.com/solaria-sh/solaria/issues/28)) ([1b40379](https://github.com/solaria-sh/solaria/commit/1b40379558720d72735f85e5bf587277dbfb3882))


### Fixed

* build with Go 1.27.4 for GO-2026-6859 ([#29](https://github.com/solaria-sh/solaria/issues/29)) ([d8822c1](https://github.com/solaria-sh/solaria/commit/d8822c19aaca5feeac6034656808a3310b1974c1))

## [0.8.0](https://github.com/solaria-sh/solaria/compare/v0.7.0...v0.8.0) (2026-11-11)


### Added

* **vault:** add a Vault kind that scopes credentials to agents ([8a65091](https://github.com/solaria-sh/solaria/commit/8a65091f6617e4af9c5fa75c66a5cd35f7bd85bc))

## [0.7.0](https://github.com/solaria-sh/solaria/compare/v0.6.0...v0.7.0) (2026-11-10)


### Added

* **environment:** let overrides narrow an agent's tools and permissions ([7c67c50](https://github.com/solaria-sh/solaria/commit/7c67c50f58aceb8a6839d7d057f395e121b7f04a))

## [0.6.0](https://github.com/solaria-sh/solaria/compare/v0.5.1...v0.6.0) (2026-11-10)


### Added

* **init:** scaffold new projects from templates ([2c6bf92](https://github.com/solaria-sh/solaria/commit/2c6bf925688f6fc73c97af75e53c7b1035a00181))
* **site:** serve the JSON Schemas at their $id URLs ([0354935](https://github.com/solaria-sh/solaria/commit/0354935b5ffd4aa5f1dce373c43ec3556f593d30))


### Fixed

* **init:** detect a file in place of a template directory on Windows ([8077677](https://github.com/solaria-sh/solaria/commit/8077677579667c512304dd65037d39bd4b03ed9e))

## [0.5.1](https://github.com/solaria-sh/solaria/compare/v0.5.0...v0.5.1) (2026-11-10)


### Fixed

* **cli:** name local processes among the hosts in solaria --help ([424a159](https://github.com/solaria-sh/solaria/commit/424a159437cbba85687cd68bdb99e5986ddafc89))

## [0.5.0](https://github.com/solaria-sh/solaria/compare/v0.4.0...v0.5.0) (2026-11-10)


### ⚠ BREAKING CHANGES

* **status:** status -o json no longer prints the raw state; see docs/concepts.md#starting-an-agent for the new shape.

### Added

* **status:** show how to start an agent ([e1d22df](https://github.com/solaria-sh/solaria/commit/e1d22dfe872bfda62383f2005d8519efcc874249))


### Fixed

* **claude-agent-sdk:** keep the docker SDK layer cached across card changes ([dda906a](https://github.com/solaria-sh/solaria/commit/dda906a6ebc1f2346cd31eaa26a7b4fbc744364e))

## [0.4.0](https://github.com/solaria-sh/solaria/compare/v0.3.0...v0.4.0) (2026-11-10)


### Added

* **claude-agent-sdk:** add the docker placement ([18ba778](https://github.com/solaria-sh/solaria/commit/18ba778bf6aeb1330962180f25ad5233bc3569ec))

## [0.3.0](https://github.com/solaria-sh/solaria/compare/v0.2.0...v0.3.0) (2026-11-10)


### Added

* **process:** add the hermetic process placement's isolation ([7c2cf72](https://github.com/solaria-sh/solaria/commit/7c2cf72a9214ade5e56ff63b6cbab5ff96a77443))
* **providers:** add the claude-agent-sdk provider on the process placement ([8f4eb38](https://github.com/solaria-sh/solaria/commit/8f4eb38d79752ba9b887d7b7a1bf40a0a4bf888f))

## [0.2.0](https://github.com/solaria-sh/solaria/compare/v0.1.0...v0.2.0) (2026-11-10)


### Added

* **cli:** add solaria digest ([472c7f6](https://github.com/solaria-sh/solaria/commit/472c7f6c98433eb2a360b12a3d8e4462ed24bf77))
* pin environments' digests with solaria.lock ([17278ed](https://github.com/solaria-sh/solaria/commit/17278ed281ead822708be1aa7c36d3f0cbdae271))

## [0.1.0] - 2026-11-08

### Added

- Repository foundation: license, contributing guide, code of conduct, security policy.
- Architecture decision records 0001–0012.
- `solaria version`, which also works out the version, commit and date of builds made with `go install`.
- M1 spike notes for gitagent v3 and Claude Managed Agents.
- `solaria.sh/v1alpha1` resource types: Agent, Environment, McpServer.
- JSON Schema for every kind in `schemas/v1alpha1/`, generated from the Go types, and `solaria schema` to print it.
- `solaria validate`: schema checks, references, instruction and skill files, and portability warnings, each with file, line and column. `-o json` for tools.
- Environment resolution: overrides (field replacement, JSON Merge Patch for provider fields), references, label selectors, and `specDigest`/`resolvedDigest` over a versioned, normalized definition (`solaria.sh/definition/v1`).
- Local state per environment (`.solaria/state/<env>.json`) with lineage, serial, history, lock files and stale-write protection.
- `solaria plan`, `apply`, `status`, `destroy` and `force-unlock`. Plans check provider capabilities before any host call, refresh to detect drift and deleted objects, and show field-level changes and digests. `plan --detailed-exitcode` for CI.
- `stub` provider, which keeps host objects in files, and `examples/hello`.
- `gitagent` provider (placement `docker`): renders the card into gitagent's config layer, generates a Dockerfile from the environment's sandbox on a cloud-like Ubuntu 24.04 base, builds `solaria/<env>/<agent>:<digest>` with the docker CLI, and records a ready `docker run` command. Plans rebuild when the sandbox changes, and refresh detects images rebuilt or removed outside Solaria.
- Environment `providers.<name>` for host settings, such as `providers.gitagent.platform`.
- `managed-agents` provider (placements `cloud` and `self-hosted`): creates one Managed Agents environment per Solaria environment, uploads skills pinned by version, and creates or updates agents with version checks, logical IDs and digests in their metadata. On `self-hosted` it builds and runs a worker container on the Solaria sandbox base; the first apply stops with Console instructions for the environment key. Refresh detects agents and environments changed or archived outside Solaria, and destroy archives agents.
- Environment-level host objects (ADR 0012): plans, state and `status` show an object the environment's agents share, such as the Managed Agents environment, as `Environment/<name>`.
- `ma-cloud` and `ma-self-hosted` environments in `examples/hello`.
- `examples/reviewer`: one agent deployed unchanged to gitagent, Managed Agents cloud and Managed Agents self-hosted.
- Documentation: concepts, getting started, a reference page per resource generated from the schema with `make schema`, and a page per provider.
- Release tooling: GoReleaser builds binaries, checksums and SBOMs, a multi-platform image on GHCR and a Homebrew cask, with keyless cosign signatures; a tag starts the release workflow, which leaves a draft release to review.
- The solaria.sh website, with the documentation and install scripts for macOS, Linux and Windows (`install.sh`, `install.ps1`), and the `solaria-sh/tap` Homebrew tap.

[0.1.0]: https://github.com/solaria-sh/solaria/releases/tag/v0.1.0
