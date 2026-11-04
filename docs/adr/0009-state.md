---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# State: local per environment, locking, backend interface

## Context and problem statement

`plan` needs to know what is deployed where, and under which host IDs and versions. Several people may work on the same project, each with their own environments, and shared environments (staging, production) must not be changed by two writers at once.

## Decision drivers

- Correctness when several people use the same project.
- Room to add remote backends later without changing the format.
- No secrets in state.
- Simple enough for v0.

## Considered options

- Local, gitignored state files.
- State committed to git.
- Remote state from day one.

## Decision outcome

Chosen option: "Local, gitignored state per environment, behind a backend interface". The design follows Terraform:

- **File:** `.solaria/state/<environment>.json`. It holds `format`, `lineage` (a UUID fixed when the state is created) and `serial` (incremented on every write). Each resource entry records:
  - the provider and its version;
  - `specDigest` and `resolvedDigest`;
  - the host binding and outputs;
  - the source commit, and whether the worktree was dirty;
  - when it was applied and by whom;
  - a `history` of earlier bindings.
- **Locking:** `.solaria/state/<environment>.lock` holds a nonce. Every command that writes takes the lock, and `solaria force-unlock <nonce>` releases a stuck one.
- **Stale writes:** a write is refused when its lineage differs or its serial is behind.
- **No secrets:** state holds only reference names, so it's safe to read and share, unlike Terraform's state.
- **`StateBackend` interface from day one:** the local backend is the only implementation in v0. Remote backends with real locking (Azure Blob leases, S3, HTTP) come later.

**Working practice:** every developer has personal environments (`local-<name>`), with name-prefixed host objects so nobody collides. Shared environments are applied from one place only: CI once it exists, one agreed person until then.

**Rollback:** check out an earlier commit and apply it. If `history` already holds a binding with the same `resolvedDigest`, a provider may point back to it instead of creating a new host version. Managed Agents can do this, because sessions pin `{id, version}`.

### Consequences

- Good, because there's no infrastructure to set up for v0.
- Good, because the format and the interface are ready for remote backends.
- Bad, because local state doesn't protect shared environments by itself; working practice does, until a remote backend exists.

### Confirmation

End-to-end tests cover a lock conflict, a stale serial and a lineage mismatch.

## More information

- [Terraform state locking](https://developer.hashicorp.com/terraform/language/state/locking)
- [Managed Agents: pinning a session to an agent version](https://platform.claude.com/docs/en/managed-agents/sessions)
