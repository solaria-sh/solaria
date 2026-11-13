---
status: accepted
date: 2026-11-08
decision-makers: Renan Tarouco da Fonseca
---

# A committed lock file pins each environment's digests

> [ADR 0020](0020-targets-and-environments.md) renamed the `Environment` kind this ADR calls an environment to `Target`, and `-e/--environment` to `-t/--target`. Lock format `solaria.sh/lock/v2` keeps pins under `targets`, and v1 is still read.

## Context and problem statement

State is gitignored and records what is deployed (ADR 0009). Teams that promote agents through stages want the opposite record too: what each environment is allowed to run, reviewed in a pull request before anything is applied. Promotion then means moving an environment to a new `resolvedDigest` and having someone approve that change. Stages also run different versions at once, for example a new version in `shadow` while `propose` keeps the old one. How does a project record, and enforce, the digest each environment may run?

## Decision drivers

- Promotion is a reviewed diff in git, not a command someone runs on a laptop.
- Nobody types or copies digests by hand.
- A mismatch fails at plan time, before any host call, and says what to do.
- Environments that nobody pins (personal and dev environments) keep working as today.
- It's an assertion, not a policy: Solaria checks the pin and never decides when to move it (ADR 0010).

## Considered options

- A committed lock file, written by a `solaria lock` command.
- A pin field in the Environment (`spec.pins`), as GitOps tools pin image digests in an overlay.
- A pin field plus a lock file that records the known digests.
- An environment that tracks a git revision, and resolves definitions from that commit.

## Decision outcome

Chosen option: "A committed lock file, written by a `solaria lock` command", because promotion becomes one generated diff to review, and no digest is ever written by hand.

- **File:** `solaria.lock` in the working directory, next to `.solaria/`, committed to git. `--lock-file` points elsewhere, like `--state-dir`. It's JSON with a fixed key order (environments and agents sorted), two-space indentation and a final newline, so the same content always produces the same bytes and diffs stay small:

  ```json
  {
    "format": "solaria.sh/lock/v1",
    "definitionFormat": "solaria.sh/definition/v1",
    "environments": {
      "propose": {
        "agents": {
          "Agent/triage": {
            "specDigest": "sha256:…",
            "resolvedDigest": "sha256:…",
            "commit": "4f1c2e9…"
          }
        }
      }
    }
  }
  ```

  `commit` is the source commit the digests were computed from, left out when the worktree was dirty. `definitionFormat` is `resolve.Format` at the time of writing. There are no timestamps or user names, so two people locking the same content write the same file.

- **Writing:** only `solaria lock -e <env> [-a <agent>]...` writes the file. It resolves the environment, computes the digests exactly as `plan` does, and records them. It reads no state and calls no host. Agents are named with `-a`, repeatable, because positional arguments are paths on every command. With `-a` it updates only those entries, and `-a` with an agent the environment no longer selects removes its entry; without `-a`, it records every agent the environment selects and drops entries for agents it no longer selects. An entry whose digests haven't changed is kept as it is, `commit` included, so moving HEAD alone changes nothing. The lock file itself doesn't make the worktree dirty. `solaria lock -e <env> --remove` removes the environment, which unpins it. `apply` never writes the lock.
- **Checking:** for an environment in the lock, `plan` and `apply` fail before refreshing when:
  - an agent's `resolvedDigest` differs from the lock;
  - the environment selects an agent the lock doesn't list (it hasn't been promoted yet);
  - the lock lists an agent the environment no longer selects (its removal hasn't been promoted).

  Each diagnostic names the lock file, the environment and the agent, shows the locked and current digests, and ends with the `solaria lock` command that would accept the change. Environment-level changes that aren't part of any digest, such as the sandbox or telemetry, aren't checked.
- **Unpinned environments:** an environment absent from the lock is not checked at all.
- **`validate`** checks the file's format and that every environment in it exists. A lock naming an agent that no longer exists is reported by `plan` as above.
- **`status`** adds a `LOCKED` column, with each pin's digest, for a locked environment.
- **`destroy`** doesn't read the lock. It works from state, as before (ADR 0009).
- **Format changes:** when `definitionFormat` differs from the running Solaria's `resolve.Format`, every digest has changed. `plan` fails once for the whole file and says to run `solaria lock` again for each environment, rather than reporting every agent. `solaria lock` writes the new format and warns about the other environments in the file that still need locking again.
- **Promotion** is: run `solaria lock -e propose -a triage`, commit, open a pull request, and apply from CI after the merge. Whether to promote, and when, stays with the team (ADR 0010).
- **Two versions at once:** a locked environment whose source has moved on can't be applied, so it stays as deployed until it's promoted. The old version keeps running on its host, because the host binding in state still points to it.

### Consequences

- Good, because a promotion pull request shows exactly which agents change in which environment, and nothing else.
- Good, because the check needs only the configuration, so CI can run it without credentials.
- Good, because environments that nobody locks behave exactly as before.
- Bad, because "lock" now means two things: this file, and the state lock with its `force-unlock` command (ADR 0009). Terraform lives with the same overlap (`.terraform.lock.hcl` beside state locking). If it confuses people, `solaria pin` and `solaria.pins` are the alternative names.
- Bad, because a locked environment can't take any change to its agents until it's promoted, including an urgent fix. The fix is promoted like anything else.
- Bad, because a locked environment can't be rebuilt from the current checkout once its source has moved on. If its host object is lost, the old version has to be applied from the commit recorded in the lock.

### Confirmation

Testscript scripts cover `solaria lock`, a matching plan, each of the three mismatches, a removed environment and a `definitionFormat` change. A golden test fixes the file's bytes.

## Pros and cons of the options

### A pin field in the Environment

- Good, because it's how GitOps tools pin a deployed version: an image digest in the per-environment overlay.
- Bad, because digests are copied by hand into YAML.
- Bad, because the pin sits next to the overrides that change the digest it pins, so editing one easily breaks the other.

### A pin field plus a lock file

- Good, because the YAML states the intent and the lock keeps the record.
- Bad, because two files must agree, and digests are still typed by hand.

### An environment that tracks a git revision

- Good, because two stages can run different versions from one checkout and either can be rebuilt at any time, as Argo CD does with `targetRevision`.
- Bad, because resolving from git history becomes part of every plan, which is much more work and couples Solaria to git.
- Neutral: it can be added later on top of this lock, since `commit` already records where each digest came from.

## More information

- [ADR 0008](0008-agent-identity.md): the digests.
- [ADR 0009](0009-state.md): state, history and rollback.
- [ADR 0010](0010-promotion-policy-out-of-scope.md): promotion policy stays out of Solaria.
- [Terraform dependency lock file](https://developer.hashicorp.com/terraform/language/files/dependency-lock), which pins provider versions rather than deployed objects, and [Argo CD tracking strategies](https://argo-cd.readthedocs.io/en/stable/user-guide/tracking_strategies/) for the git-revision option.
