# Changelog

All notable changes to this project are documented here. The project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

After 0.1.0, [release-please](https://github.com/googleapis/release-please) writes this file from the Conventional Commit messages on `main`: `feat` under Added, `fix` under Fixed, `perf` and `revert` under Changed, breaking changes first. Don't edit it by hand; see [Releasing](CONTRIBUTING.md#releasing) for how to change an entry. 0.1.0 was written by hand in the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format.

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
