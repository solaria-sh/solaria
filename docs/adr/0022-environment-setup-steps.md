---
status: proposed
date: 2026-11-26
decision-makers: Renan Tarouco da Fonseca
---

# Environments run setup steps before the agent starts; repositories stop being a primitive

## Context and problem statement

ADR 0017 made git repositories a run input. An Agent declares `repositories` with an access, a Target binds each one to a URL, a ref and a `gitHost` credential, and each host clones them its own way: the launcher on `process` and `docker`, and a `github_repository` resource on Managed Agents cloud. gitagent and the Managed Agents self-hosted worker can't clone, so a repository fails plan there.

Cloning a repository is one step of one kind of workflow, a coding agent's. Other workflows need other preparation: fetching a dataset, checking out a branch and installing its dependencies, generating a config file, warming a cache. Each one needs the same thing, which is to get the workspace ready deterministically before the agent's first turn, so the agent doesn't spend tokens and turns doing it. Solaria describes agentic workflows in general (ADR 0002), so it shouldn't build one workflow's step into its resource model and leave the others to the agent.

How does a definition prepare an agent's environment before a run, on every host Solaria's code runs on?

## Decision drivers

- Solaria stays general: no workflow-specific primitive where a general one does the job.
- Preparation is deterministic and needs no reasoning, so it runs before the agent, not as the agent's first turns.
- A sandbox is described once and reused (ADR 0020), so preparation that belongs to the sandbox is described with it.
- What changes between stages, such as a repository's URL or a token, belongs to the Target, as bindings do today (ADR 0017).
- Secrets used to prepare a run never reach the run, the state, the plan output or the logs.
- Fail closed: a host that can't run setup fails plan instead of starting an unprepared agent.
- The break happens while the API is `v1alpha1`, and every change it forces is spelled out.

## Considered options

- Environment `spec.setup`: named shell steps that run before the agent starts.
- Agent `spec.setup`: the same steps, declared by the agent.
- Generalize `repositories` into typed inputs (git, HTTP archive, object storage).
- Keep `repositories`, and add setup steps next to them.

## Decision outcome

Chosen option: "Environment `spec.setup`". Setup prepares the sandbox an agent runs in, which is what an Environment describes (ADR 0020), so one Environment's setup is reused by every target and agent that uses it. Shell steps cover cloning and every other kind of preparation with one mechanism, and nothing in Solaria knows about git any more.

### Declaring setup

```yaml
kind: Environment
metadata: {name: product}
spec:
  packages: {apt: [git]}
  setup:
    - name: clone
      run: git clone --depth 1 --branch "$REF" "$REPO_URL" product
      env:
        - {name: REPO_URL}                     # no value: every target must set it
        - {name: REF, value: main}             # a default
        - {name: GH_TOKEN, secretRef: GH_TOKEN}
    - name: deps
      run: cd product && npm ci
```

- `setup` is a list of steps, run in order. Each has a `name`, unique in the Environment and a DNS label like other names, a `run` script, and an optional `env`.
- A step runs as `sh -e -c <run>` with the workspace as its working directory. Its output goes to the run's stderr, never the agent's context.
- `env` entries take a `value`, a `secretRef` (a name in the target's secret source), or neither. With neither, every target that uses the Environment must set the variable, or `validate` fails, naming the target, the Environment and the step.
- A step gets the run's base environment (`PATH`, `HOME` and the like, as the launcher's allowlist gives it) plus its own `env`. It gets none of the run's secrets and no other step's.
- `packages` stay build-time and `setup` is run-time. Steps run on every run, after the sandbox is built and before the agent's first turn.

### Per-target values

A Target sets the values that change between stages:

```yaml
kind: Target
spec:
  inputs:
    setup:
      env:
        - {name: REPO_URL, value: https://dev.example.com/org/product.git}
        - {name: GH_TOKEN, secretRef: PRODUCT_TOKEN}
```

- An entry overrides the variable of that name in every step of every Environment the target uses. A name no step declares fails `validate`, so a typo can't go unnoticed.
- One list per target is enough while a target's Environments don't share variable names with different meanings. Values per Environment can come later without a break, under `inputs.setup.environments.<name>.env`.

### Running setup

Setup is a capability of each placement, like the limits (`Capabilities.Setup`). An Environment with setup, bound to an agent on a placement without it, fails plan, naming the target, the Environment and the agent.

| Placement | Who runs setup | When |
|---|---|---|
| `process` | The launcher, on the machine itself | Before the run starts. With `sandbox.isolation: srt`, before srt starts, as clones do today (ADR 0017) |
| `docker` (claude-agent-sdk) | The launcher, inside the container | Before the run starts |
| gitagent | An entrypoint script Solaria writes into the image, which then `exec`s gitagent | Before gitagent starts |
| `managed-agents` / `self-hosted` | The worker, from `ToolsFunc`, once per session | Before the session's first tool call is served |
| `managed-agents` / `cloud` | Nothing: no Solaria code runs before a session | Setup fails plan |

- **The workspace.** Steps run in the workspace as it is. Solaria doesn't empty it, so a step that runs twice in the same workspace has to handle its own earlier output (`[ -d product ] || git clone …`). This matters on `process`, where the workspace is a folder that outlives runs, and on the self-hosted worker, where the SDK gives every session the same workdir and keeps its own `skills/` and `memory/` folders there.
- **A read-only workspace.** On `process` with srt, setup runs before srt, so it can write a workspace the agent then can't. On `docker` and gitagent the workspace is mounted `:ro`, so setup can't write it, and a read-only workspace with setup fails plan there, offering `process` with `sandbox.isolation: srt` or a read-write workspace. On Managed Agents a read-only workspace already fails plan.
- **Where setup lives.** The steps are rendered into the launcher's bundle (the file that lists clones today), the gitagent entrypoint, or the worker's config, never into the sandbox base. So editing a step rebuilds the launcher layer, not the packages. The target's plain values are rendered next to them. Secret values are read from the target's secret source at run time, as clone tokens are today.
- **The self-hosted worker.** A worker serves one target and Environment pair (ADR 0020), so its steps and values are fixed when apply builds it, and it needs no lookup per session. `ToolsFunc` is the SDK's only hook that runs per session before tools are served, so the worker runs setup there. A step's secrets reach the worker container through docker's environment, as MCP server secrets do (ADR 0021), and the agent's `bash` doesn't get them.

### Failure

A step that exits non-zero stops the run before the agent starts:

- The launcher prints a result with `subtype: error_setup`, naming the step and its exit code, and exits 2, the code for a run that couldn't start. The step's output is already on stderr.
- gitagent's entrypoint prints the same message to stderr and exits 2.
- On the self-hosted worker, `ToolsFunc` can't fail the session, so every tool call in that session returns the same error, and the agent ends without having touched the workspace.
- Nothing retries a step. A run that should retry says so in its script.

### Secrets

The target's secret source supplies only the secrets that a step's `env` names, and only to that step. The launcher builds the run's environment from its allowlist after setup, so the run never sees them. gitagent's entrypoint unsets them before it `exec`s gitagent, so they're not in gitagent's environment or in `/proc/1/environ`. The worker drops them from `bash`'s environment, as it drops MCP server secrets. State, plans, the lock file and rendered files hold a secret's name, never its value.

A step's script is a definition someone reviewed, like a package list, and it runs with the run's user and, under srt, outside the sandbox. The docs say so: setup is trusted code, and what it fetches isn't.

### Digests and plans

An agent's digests don't change. Which sandbox an agent gets, and how it's prepared, is the target's decision (ADR 0020), not part of what the agent is. The bundle, image or worker that carries the steps changes when they do, so plan shows that as an update, with each step's name, its `run` and its variables' names and plain values, and only the name of a secret.

### Removing repositories

`repositories` goes in one breaking change, with everything that exists only for it:

- the Agent's `spec.repositories` and the Target's `spec.inputs.repositories`;
- the Vault credential target `for: {gitHost: …}`;
- Managed Agents' `github_repository` resources and the repositories that `status -a` and `status -o json` list;
- `Capabilities.Repositories` and `Capabilities.ReadOnlyRepositories`, and the clone code in the launcher preludes.

A document that still has one fails at that key with the equivalent step:

```text
agents/reviewer.yaml:12:3: spec.repositories was removed (ADR 0022); clone it in a setup step of the agent's Environment instead:
  setup:
    - name: clone-product
      run: git clone --depth 1 --branch "$REF" "$REPO_URL" repositories/product
      env: [{name: REPO_URL}, {name: REF, value: main}]
and set REPO_URL in the target's spec.inputs.setup.env
```

The error for `inputs.repositories` and `gitHost` points to the same place. There's no grace period: the field is `v1alpha1`, and decoding it for a release would mean keeping the clone code that this decision removes.

`resolve.Definition` loses `repositories`. It was omitted when empty, so agents without repositories keep their digests and `resolve.Format` doesn't change. Agents that had them fail to decode until they're migrated, and ADR 0008 records the removal.

`workspace.access` and `inputs.workspace.path` stay as ADR 0017 describes them. So does the srt isolation of `process`.

### Consequences

- Good, because any workflow can prepare its workspace before the agent starts, not only coding ones, and an agent doesn't spend turns on preparation a script can do.
- Good, because setup works on any git host and with any tool the Environment's packages provide, on gitagent and the self-hosted worker too, where repositories failed plan.
- Good, because Solaria loses its git-specific code: the clone preludes, the `gitHost` credential target and the `github_repository` mapping.
- Good, because a step names exactly the secrets it uses, and no other step or the run sees them.
- Bad, because a step is a shell script, so Solaria can't check what it does, and a mistake shows up at run time rather than at plan time.
- Bad, because per-repository access is gone. A read-only clone next to a writable workspace can't be declared any more. A read-only workspace prepared by setup is the closest thing, and it works only on `process` with srt.
- Bad, because Managed Agents cloud loses repository attachment through Solaria. Callers attach GitHub repositories when they start a session, which they did before too, but `status` no longer tells them which.
- Bad, because steps aren't emptied between runs, so they must cope with a workspace they already prepared.
- Bad, because every project with `repositories` has to change, and old documents fail rather than warn.

### Confirmation

- Schema and decode tests cover `spec.setup`, `inputs.setup.env`, a required variable no target sets, an unknown target variable, and the migration error for each removed field.
- Plan tests cover each fail-closed case: setup on Managed Agents cloud, and a read-only workspace with setup on `docker` and gitagent.
- Launcher tests on `process` and `docker` cover step order, a failing step giving `error_setup` and exit 2, a step's output on stderr, and a step's secret reaching that step but not another step or the run.
- gitagent golden files show the entrypoint, and a test checks that a step's secret is gone from gitagent's environment.
- Worker tests cover setup in `ToolsFunc`, the tool error after a failed step, and a step's secret missing from `bash`.
- A test checks that a step's secret is in neither the plan, the state nor the files apply writes.
- Every embedded template still validates and plans (ADR 0015).

## Pros and cons of the options

### Agent `spec.setup`

- Good, because an agent's card would say how its workspace is prepared, and review would see it with the agent.
- Bad, because preparation would enter the agent's digests, so changing a dependency install would look like a new agent.
- Bad, because agents that share a sandbox would repeat the same steps, which is what ADR 0020 removed for packages.

### Typed inputs (git, HTTP archive, object storage)

- Good, because Solaria could check each input at plan time and enforce access per input.
- Bad, because each type is code Solaria has to write for every host, and every workflow it hasn't thought of still needs something else.

### Keep `repositories` and add setup steps

- Good, because nothing breaks.
- Bad, because there would be two ways to clone a repository, and the git-specific code and its fail-plan cases would stay.

## More information

- [ADR 0002](0002-control-plane-not-agent-framework.md): Solaria describes workflows and doesn't run agent loops.
- [ADR 0007](0007-portable-sandbox-is-packages.md): the portable sandbox, whose packages stay build-time.
- [ADR 0008](0008-agent-identity.md): the digest input, which records the removal of `repositories`.
- [ADR 0016](0016-vault-scoped-credentials.md): Vault credentials, which lose the `gitHost` target.
- [ADR 0017](0017-run-inputs.md): run inputs. This ADR replaces its repositories and keeps its workspace and srt isolation.
- [ADR 0020](0020-targets-and-environments.md): Targets and Environments.
- [ADR 0021](0021-self-hosted-worker-runs-stdio-mcp-servers.md): the self-hosted worker, its `ToolsFunc`, and how it keeps secrets out of `bash`.
- The SDK's `EnvironmentWorker` gives every session the same `Workdir`, and keeps each session's skills under `skills/` and memory stores under `memory/` there (anthropic-sdk-go v2.9.0, `lib/environments/worker.go`).
