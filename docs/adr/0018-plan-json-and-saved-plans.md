---
status: proposed
date: 2026-11-10
decision-makers: Renan Tarouco da Fonseca
---

# A documented plan JSON, and saved plans that apply only what was reviewed

## Context and problem statement

Teams that apply from CI want two things. First, a plan they can post on a pull request, which means a machine-readable plan rather than the text `plan` prints. Second, an apply that does exactly what was reviewed: if state, the configuration or a host changed between the review and the apply, the apply must stop instead of doing something nobody saw. Terraform answers both with `plan -json` and saved plan files. What should Solaria's plan JSON contain, and how does a saved plan guarantee that apply matches it?

## Decision drivers

- One format for both uses, documented like `status -o json` (ADR 0014), so programs can rely on it.
- Apply never does anything the saved plan doesn't show.
- No secrets in the file, so it can be uploaded as a CI artifact or posted in a comment.
- With a shared state backend, "only CI applies" becomes something a team can enforce.

## Considered options

- `plan -o json` only.
- `plan -o json` plus saved plans that store the full resolved definitions, as Terraform's plan files store configuration.
- `plan -o json` plus saved plans that store digests and the planned steps, checked against a fresh plan at apply.

## Decision outcome

Chosen option: "Saved plans that store digests and the planned steps, checked against a fresh plan at apply", because digests already identify exactly what an agent is (ADR 0008), so a plan file needs no copy of the definitions to prove that apply matches it.

**The plan JSON** has a format identifier, `solaria.sh/plan/v1`, and is documented in `docs/concepts.md` with the meaning of every field:
- the Solaria version and `resolve.Format`;
- the environment, provider and placement;
- the state's `lineage` and `serial` at plan time;
- the source commit, and whether the worktree was dirty;
- one step per agent (no-ops included), sorted by logical ID: action, `specDigest` and `resolvedDigest` before and after, field changes (long text is summarized, as in the text plan), reason and notes;
- the environment's host step (ADR 0012);
- the diagnostics.

Secret values never appear, only reference names, as everywhere else. Fields are added compatibly; anything that removes or changes one is a new format version.

**Commands.**
- `solaria plan -e <env> -o json` prints it.
- `solaria plan -e <env> -out <file>` writes it and also prints the text plan.
- `solaria apply <file>` applies it. It takes no `-e`: the file names the environment.

**What apply checks.** `apply <file>` takes the state lock, then plans again from the current checkout, state and hosts, and compares the result with the file. It refuses, changing nothing, when:
- the state's lineage or serial differs;
- any agent's digests, or the environment's host step, differ;
- any step's action differs, for example because a refresh found drift since the plan;
- the file's Solaria version or `resolve.Format` differs from the running binary's.

Each refusal names what changed and says to plan again. When everything matches, apply runs the steps in the file. A saved plan is single-use in practice: the apply increments the serial, so the same file is refused afterwards.

**Lock files.** An environment in `solaria.lock` (ADR 0013) is checked when the plan is made and again at apply, so a plan made before a lock change can't be applied after it.

### Consequences

- Good, because CI can post the JSON on a pull request and apply the same plan after review, with a guarantee that nothing else happens.
- Good, because the file is small and safe to share: digests and names, no definitions or secrets.
- Bad, because the apply must run against the same checkout, since the file doesn't carry the definitions. CI usually has that checkout anyway; a later version could embed the definitions if needed.
- Bad, because any drift between plan and apply means planning again, even drift that wouldn't have changed the outcome.

### Confirmation

- A golden test fixes the JSON for a plan with every kind of step.
- Testscript scripts cover `plan -out` and `apply <file>`, and refusal for each check: serial, digest, action and version.
- A test checks that no secret value appears in the file.

## Pros and cons of the options

### `plan -o json` only

- Good, because it's the smallest change.
- Bad, because CI would still apply whatever the checkout says at apply time, not what was reviewed.

### Saved plans with the full definitions

- Good, because apply wouldn't need the checkout.
- Bad, because the file would carry instructions, skills and everything else, which makes it large, and an extra copy of the definitions to keep out of logs.
- Bad, because the digests already prove identity, so the extra content adds no guarantee.

## More information

- [ADR 0008](0008-agent-identity.md): digests.
- [ADR 0009](0009-state.md): lineage, serial and locking.
- [ADR 0013](0013-lock-file-pins-digests.md): the lock file.
- [Terraform saved plans](https://developer.hashicorp.com/terraform/cli/commands/plan#out-filename) and [JSON output format](https://developer.hashicorp.com/terraform/internals/json-format).
