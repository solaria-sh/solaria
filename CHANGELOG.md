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
