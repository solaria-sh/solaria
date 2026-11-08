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
- **Writes per apply** (as implemented in M4):
  - One write per apply, so `serial` counts applies.
  - When a step fails, the steps completed before it are still written.
  - Deletes run first, then the other steps by logical ID.
  - `plan` only reads, so it takes no lock.
  - The local backend writes `.solaria/.gitignore` (`*`), so state stays out of git without setup.
  - `destroy` works from state alone, even after the environment left the configuration.
- **No secrets:** state holds only reference names, so it's safe to read and share, unlike Terraform's state.
- **`StateBackend` interface from day one:** the local backend is the only implementation in v0. Remote backends with real locking (Azure Blob leases, S3, HTTP) come later.

**Working practice:** every developer has personal environments (`local-<name>`), with name-prefixed host objects so nobody collides. Shared environments are applied from one place only: CI once it exists, one agreed person until then.

**Rollback:** check out an earlier commit and apply it. How that lands depends on the host:

- **Managed Agents** can't revert an agent's "latest". Re-applying old content creates version N+1 with the old `resolvedDigest`, and `history` records that both versions share it. Consumers that pin sessions to `{id, version}` read the current binding from state, so they can also pin the older version directly.
- **Container placements** re-tag or rebuild the image for that digest.

**Drift:** each entry also stores a `remoteHash`, a hash of the host's normalized response as of the last apply (an idea taken from `ant apply`'s `claude-lock.json`). A refresh that finds a different hash reports drift, such as a change made in a console. Normalization fills in server-side defaults first, so they don't show up as false drift.

**Environment-level host objects** (since M6, ADR 0012): `host` holds one more entry, for an object the environment's agents share, such as a Managed Agents environment. It has the same fields as an agent entry, apart from the digests and definition, which become the provider's own config map and its digest, and the history, which it doesn't keep.

**Drift as implemented:** providers choose what `remoteHash` is. `gitagent` uses the image ID. `managed-agents` uses the agent's version, which the host increases on every change, and the environment's `updated_at`; neither needs normalization.

**Destroy is host-specific and shown as such in the plan.** For example, Managed Agents agents can only be archived, irreversibly, so plan shows `-/archive`.

### Consequences

- Good, because there's no infrastructure to set up for v0.
- Good, because the format and the interface are ready for remote backends.
- Bad, because local state doesn't protect shared environments by itself; working practice does, until a remote backend exists.

### Confirmation

End-to-end tests cover a lock conflict, a stale serial and a lineage mismatch.

## More information

- [Terraform state locking](https://developer.hashicorp.com/terraform/language/state/locking)
- [Managed Agents: pinning a session to an agent version](https://platform.claude.com/docs/en/managed-agents/sessions)
