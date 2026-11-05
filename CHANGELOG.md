# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Repository foundation: license, contributing guide, code of conduct, security policy.
- Architecture decision records 0001–0010.
- `solaria version` command.
- M1 spike notes for gitagent v3 and Claude Managed Agents; ADR 0011 (the `managed-agents` provider calls the API directly).
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
