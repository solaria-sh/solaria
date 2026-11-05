---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# The `managed-agents` provider calls the API directly

## Context and problem statement

Anthropic's `ant` CLI (v1.30.0 and later) includes `ant apply`. It's a declarative workflow for Managed Agents resources: agents as Markdown files with frontmatter, environments, skills, vaults and deployments. It has a plan and approval step, `--dry-run`, `--prune`, and a committed `claude-lock.json` that records `{kind, id, version, hash, remote_hash}` for each file. Should Solaria's `managed-agents` provider render files for `ant apply` and run it, or call the API itself?

## Decision drivers

- Full control over what Solaria guarantees: metadata with logical ID and digests, explicit skill version pins, version history for rollback, normalization for drift.
- Unit-testable without the network.
- Few external runtime dependencies for users.
- Low maintenance as the beta API changes.

## Considered options

- Call the API through the Go SDK (`github.com/anthropics/anthropic-sdk-go/v2`).
- Render `ant apply` files and run `ant apply`.

## Decision outcome

Chosen option: "Call the API through the Go SDK", because Solaria's guarantees depend on controlling each request and recording each response in its own state. It can be tested with `httptest` fakes, and users don't need a particular `ant` version (1.21.0 was installed on the reference machine; `ant apply` needs 1.30.0).

What we take from `ant apply`:

- the `remote_hash` idea, which becomes `remoteHash` in state (ADR 0009);
- a plan display that shows each field's diff.

`solaria export --format ant` may come later, for teams that want to hand a project over to `ant apply`.

**As implemented in M6:** the provider uses the SDK's typed request params rather than raw JSON, so an SDK update that renames a field fails to compile instead of failing at apply. Drift needs no normalization: the agent's version and the environment's `updated_at` change with every change on the host. Rollback works as ADR 0009 describes: old content becomes a new version. Details are in ADR 0006, and the shared environment in ADR 0012.

### Consequences

- Good, because one engine and one state format work across all providers.
- Good, because the README can position Solaria clearly: `ant apply` covers one host, Solaria covers many with a neutral card and one identity per agent.
- Bad, because Solaria has to track a beta API's changes itself, through Dependabot updates of the SDK and golden request tests.

### Confirmation

Golden transcripts of the requests for each resource and operation, made against a fake API (`httptest`).

## Pros and cons of the options

### Render files for `ant apply`

- Good, because there's less code, and it follows Anthropic's schema automatically.
- Bad, because it depends on an external CLI's version and output format.
- Bad, because a second lockfile per environment would duplicate Solaria's state.
- Bad, because it gives less control over metadata, skill pinning and rollback.

## More information

- [`ant apply`](https://platform.claude.com/docs/en/cli-sdks-libraries/cli/apply)
- [Managed Agents API spike](../spikes/2026-11-managed-agents-api.md)
