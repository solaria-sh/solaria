---
status: proposed
date: 2026-11-12
decision-makers: Renan Tarouco da Fonseca
---

# Targets bind agents to hosts; an Environment is a reusable sandbox

## Context and problem statement

Solaria's `Environment` kind (ADR 0004) is a deployment target. It picks a provider and placement and selects agents. It holds the overrides, secrets, telemetry and input bindings, and it has its own state, lock and state backend (ADR 0009). It also embeds one `sandbox`: packages, network, resources, image and isolation (ADR 0007).

Managed Agents uses the same word for something smaller. A Managed Agents environment is only a sandbox template, either `cloud` with `packages` and `networking` or `self_hosted`. It knows nothing about agents, and any agent can run in any environment, because each session picks the pair. The `managed-agents` provider already maps a Solaria environment's `sandbox` onto one Managed Agents environment, and plans it as `Environment/<name>` (ADR 0012). So the word means two things in Solaria. The second meaning, a reusable sandbox, isn't specific to Managed Agents. Container placements build it as an image, and the `process` placement turns it into srt settings.

Embedding the sandbox also fixes the pairing at one sandbox per target. Two agents in one target can't use different sandboxes, and three targets that want the same sandbox repeat it three times, as `examples/reviewer/environments.yaml` does.

Should Solaria keep one kind for both meanings, or split them? If it splits them, what does each half get called?

## Decision drivers

- Each word means one thing in Solaria's own docs, plans and state.
- The sandbox is described once, in its portable form (ADR 0007), and reused.
- A target can give different agents different sandboxes, as hosts allow.
- No host's model is copied unless it is portable. Solaria isn't a front end for Managed Agents.
- Digests don't change: the sandbox is where an agent runs, not what it is (ADR 0004, ADR 0008).
- The break happens while the API is `v1alpha1` and before 1.0, and every change it forces is spelled out.

## Considered options

- Keep `Environment` as it is, and document how it differs from a Managed Agents environment.
- Keep `Environment` as the target, and add a `Sandbox` kind for the reusable template.
- Rename the target kind to `Target`, and make `Environment` the reusable sandbox template.
- Rename the target kind, and keep the sandbox inline for now.

## Decision outcome

Chosen option: "Rename the target kind to `Target`, and make `Environment` the reusable sandbox template". It splits the two meanings, makes the sandbox reusable and lets one target hold several sandboxes. It also gives the word "environment" the meaning it already has in the plan and on the host that ADR 0012 introduced.

### `Target`

`Target` is today's `Environment` with `sandbox` replaced by references:

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Target
metadata: {name: ma-cloud}
spec:
  provider: managed-agents
  placement: cloud
  environments:
    - ref: base                     # no agents: the default
    - ref: data
      agents: {selector: {matchLabels: {sandbox: data}}}
  secrets: {source: env}
  overrides: { … }
  inputs: { … }
  providers:
    managed-agents: {namePrefix: reviewer-example-}
```

- The other fields don't change: `provider`, `placement`, `agents`, `secrets`, `telemetry`, `inputs`, `overrides`, `state` and `providers`. They keep what ADRs 0004, 0006, 0009, 0016 and 0017 say about them. State stays per target, so `state` picks the target's backend, as it does for an environment today.
- `environments` assigns Environments to the agents the target selects:
  - each entry is a `ref:` with an optional `agents` selector, the same `AgentSelection` the target uses;
  - at most one entry has no `agents`, and it is the default;
  - each agent gets the entry whose selector matches it, or else the default;
  - an agent that matches two entries, or no entry when there's no default, fails `validate`, which names the target, the agent and the entries.
- When `environments` is omitted, every agent gets the built-in default sandbox, the same as omitting `sandbox` today. That's the sandbox base image, with network `limited` to the hosts the agents need. On hosts that keep an object for it, it's planned as `Environment/default`, so `default` is reserved and an Environment can't use that name.
- `spec.sandbox` is removed. ADR 0004 says inline definitions of a kind aren't allowed once the kind exists, so there's only one way to declare a sandbox.
- "Target" is used throughout: in docs, plan output, state and the CLI. The ADR 0006 sentence "'local' is an environment" becomes "'local' is a target".

### `Environment`

`Environment` is a config kind whose spec is today's `SandboxSpec`, unchanged:

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Environment
metadata: {name: base}
spec:
  packages: {apt: [ripgrep]}
  network: {mode: limited, allow: [github.com], packageManagers: true}
  # resources, image and isolation as in ADR 0007 and ADR 0014
```

- It doesn't say where it runs. The target's placement decides how it's built: an image for `docker`, srt settings for `process`, a `cloud` or `self_hosted` Managed Agents environment.
- Capability checks don't change. They now run once per target and Environment pair, and are reported on the Environment's file and on the target's reference. `image` on `managed-agents/cloud` is an example.
- An Environment no target references gets a warning from `validate`, not an error, so a project can keep a sandbox it isn't using yet.
- Agents don't reference Environments. Which sandbox an agent gets is the target's decision, as the network policy is today. So `resolve.Definition` doesn't change, and no digest changes.

### Host objects

ADR 0012's `EnvironmentHost` stays, but the engine calls it once for each Environment that a target's agents use, instead of once per target.

- **Plan:** one step per pair, `Environment/<name>`, above the agents, or below them for a delete. The step ID finally names the Solaria resource it comes from.
- **Managed Agents:**
  - one Managed Agents environment for each target and Environment pair, named after both behind `namePrefix`;
  - an Environment is not shared across targets, so each target's state stays separate (ADR 0009);
  - the provider derives `allow_mcp_servers` from the agents bound to that Environment, not from every agent in the target;
  - on `self-hosted`, each pair has its own worker, `solaria-<target>-<environment>-worker`, and needs its own key from the Console;
  - agents carry `solaria_target`, and `solaria_environment` now names the Environment the agent is bound to;
  - Managed Agents pairs an agent with an environment only when a session starts. So `status` lists the environment ID to pass to `sessions.create` for each agent, as it already lists vault IDs (ADR 0016).
- **Container and process placements:** the sandbox base stage is shared per Environment, and images keep the `solaria/<target>/<agent>` name. That is the same value as before, so nothing is rebuilt.
- **Lifecycle:** an Environment's host object exists while the target binds at least one agent to it on a provider that keeps such objects. Unbinding the last agent deletes it, after the agents (ADR 0012's apply order).

### Migration

Nothing about the agents changes: specs, definitions and digests stay as they are. The break is in documents, the CLI, state and the lock file.

- **Documents:** a `kind: Environment` with `spec.provider` fails to decode. The error names the file and line, and says to rename the kind to `Target` and move `sandbox` into an Environment it references. Decoding the old kind under the reused name for a release would leave `kind: Environment` with two meanings while the docs explain one, so there's no grace period. The kind is `v1alpha1`. The embedded templates, examples, testscripts and generated docs change in the same pull request.
- **CLI:** `-e/--environment` becomes `-t/--target` on every command that takes it. `init` is the only command that uses `-t`, for `--template`, and it takes no target. The old flag stays hidden for one minor release and warns.
- **State:** the files stay at `.solaria/state/<target>.json`, and Azure Blob state at `<prefix>/<target>.json`, with the same names. The state format is bumped:
  - `environment` becomes `target`;
  - the single `host` entry becomes `environments`, keyed by Environment name;
  - `vaults` is keyed as before.

  Older state is read and upgraded on the next write. The old `host` entry becomes the entry of the target's default Environment, or of its only one. Plan then shows an in-place update to the new host name and metadata, not a replace. For a self-hosted worker that means a new container name and the same environment ID and key.
- **Lock:** `solaria.lock` becomes format `solaria.sh/lock/v2`, with `targets` in place of `environments`. `plan` reads v1 and `solaria lock` writes v2. `definitionFormat` is unchanged, so no pin goes stale.
- **Managed Agents objects:** the first apply after the upgrade makes a metadata-only update to every agent and environment, which adds `solaria_target`. Agents get a new version with the same `resolvedDigest`, as a rollback does (ADR 0011).

### Consequences

- Good, because "environment" means one thing in Solaria: the sandbox template. That matches the plan step and the host object that already carry the name.
- Good, because a sandbox is declared once and reused across targets. The reviewer example drops from three copies to one.
- Good, because one target can give its agents different sandboxes, which Managed Agents, container and process hosts all support.
- Good, because no digest changes, and the lock's pins stay valid.
- Bad, because every project, script and CI job that uses `kind: Environment` or `-e` has to change, and old documents fail rather than warn.
- Bad, because people coming from CI/CD tools read "environment" as a deployment stage, which is what `Target` now means. The docs have to say so plainly, starting with `docs/concepts.md`.
- Bad, because Terraform users know `-target` as "apply only these resources", not "work on this target". Solaria selects agents with `-a`, and the docs say so where the flag is introduced.
- Bad, because on `managed-agents/self-hosted` a second Environment in a target means a second worker and a second Console key.
- Neutral: Managed Agents objects get a one-time metadata update, which shows in the first plan after the upgrade.

### Confirmation

- Schema tests cover the new kinds, the removed `sandbox` field and the decode error for the old kind.
- Validation tests cover environment assignment: the default, selectors, an agent that matches two entries, and an agent with no Environment.
- Engine tests with a fake hosting provider cover one host object per Environment, the upgrade of old state, and binding or unbinding an agent.
- The `managed-agents` golden transcripts cover two Environments in one target and the metadata update.
- `internal/cli/testdata/script/` covers the hidden `-e` flag and lock v1 being read.
- Every embedded template still validates and plans (ADR 0015).

## Pros and cons of the options

### Keep `Environment` and document the difference

- Good, because nothing breaks.
- Bad, because the plan step `Environment/<name>` and the Solaria kind keep meaning different things.
- Bad, because the sandbox stays one per target and copied between targets.

### Keep `Environment` as the target and add a `Sandbox` kind

- Good, because "sandbox" is already Solaria's word for it (ADR 0007), and the target kind keeps the meaning CI/CD users expect.
- Good, because nothing is renamed: only `sandbox` becomes a reference.
- Bad, because "environment" still means a host's sandbox template in the `managed-agents` provider: in its plan step, its state entry and on the host. The two meanings stay side by side.

### Rename the target kind and keep the sandbox inline

- Good, because it frees the name with the smallest change.
- Bad, because it pays for the rename without the reuse or the per-agent sandboxes, and a second break would follow when the sandbox becomes a kind.

### Names for the target kind

- **`Target`** (chosen): neutral, and reads right both for side-by-side runtimes (`local-gitagent`, `ma-cloud`) and for stages (`dev`, `prod`). Its weak spot is the Terraform `-target` flag.
- **`Stage`**: reads well for dev, staging and prod, and ADR 0004 already talks about a "shadow stage". But it suggests a promotion order, which ADR 0010 leaves out of scope, and it reads oddly for runtime comparison.
- **`Deployment`**: Managed Agents uses it for scheduled sessions.
- **`Workspace`**: taken by run inputs (ADR 0017), and Terraform's workspaces are a different idea.
- **`Host`**: Solaria already uses it for the provider's side, as in host objects and host bindings.

## More information

- [ADR 0004](0004-resource-model.md): the resource model. ADR 0006: placements, and "local" as an environment. [ADR 0007](0007-portable-sandbox-is-packages.md): the portable sandbox. ADR 0009: state per environment. [ADR 0012](0012-environment-level-host-objects.md): environment-level host objects. ADR 0013: the lock file. ADR 0016: vaults per environment. ADR 0017: input bindings. Each is revised when this is accepted.
- [Managed Agents: environments](https://platform.claude.com/docs/en/managed-agents/environments), and the [Managed Agents spike](../spikes/2026-11-managed-agents-api.md).
- Verified on 2026-11-13 against the real API: a Managed Agents environment's `name` can be changed in place, which the upgrade relies on. `POST /v1/environments/{id}` with only a new `name` returned the same ID with the new name, and a later `GET` showed it. The API reference lists `name` as an updatable field, and the `managed-agents` provider already sends it on every update. The API doesn't document whether names must be unique, so a rename shouldn't count on it.
- To verify before acceptance: the exact wording and position of the decode error for an old `kind: Environment`.
