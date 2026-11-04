---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# Go, Apache-2.0 and distribution

## Context and problem statement

Solaria is an open-source CLI in the infrastructure-as-code category. We need an implementation language, a license and a way to distribute it, all suited to an open-source project from day one.

## Decision drivers

- A single binary that installs anywhere.
- Close to the tools whose architecture Solaria follows.
- Good SDKs for the first providers (Docker and Anthropic).
- A permissive license with a patent grant.

## Considered options

- Go
- TypeScript (Node, or a compiled Bun binary)
- Rust

## Decision outcome

Chosen option: "Go", because it's what this category is written in (Terraform, OpenTofu, kubectl, Docker's cagent). It builds static binaries, and Terraform's plugin system (`go-plugin` over gRPC) is a ready model for out-of-process providers later. The Anthropic Go SDK covers the Managed Agents beta.

**License: Apache-2.0.** It's the norm for infrastructure CLIs and includes an explicit patent grant. Contributions are accepted under the same license, with a DCO sign-off (see CONTRIBUTING.md).

**Distribution:**

- GitHub repository `renantarouco/solaria`.
- Releases built with GoReleaser: binaries for macOS, Linux and Windows, checksums and an SBOM.
- Releases signed keylessly with cosign.
- Installs through a Homebrew tap, `go install` and a container image on GHCR.
- The JSON Schema for resources is published at a stable URL, and later submitted to SchemaStore for editor support.

### Consequences

- Good, because Terraform's and OpenTofu's code serves as reference for state, planning and plugins.
- Good, because a single binary is easy to use in CI and in containers.
- Bad, because iteration is slower than in TypeScript, and many AI-tooling contributors write TypeScript or Python.

### Confirmation

CI builds and tests with the Go version declared in `go.mod`. GoReleaser configuration lands in M7.

## Pros and cons of the options

### TypeScript

- Good, because it's the fastest to iterate in and closest to the AI tooling community.
- Bad, because the single-binary and plugin stories are less mature, and it's less familiar to infrastructure contributors.

### Rust

- Good, because it produces excellent binaries (gitagent is written in Rust).
- Bad, because it's the slowest to iterate on while the design is still moving.

## More information

- [Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go)
- [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin)
- [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0)
