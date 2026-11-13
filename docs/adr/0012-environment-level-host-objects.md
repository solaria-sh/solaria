---
status: accepted
date: 2026-11-08
decision-makers: Renan Tarouco da Fonseca
---

# Environment-level host objects

> [ADR 0020](0020-targets-and-environments.md) renamed the `Environment` kind this ADR calls an environment to `Target`, and `-e/--environment` to `-t/--target`. It also made the sandbox a reusable `Environment` kind, so a target now has one host object for each Environment its agents use, with the ID `Environment/<name>` of that Environment, and state keeps them under `environments`.

## Context and problem statement

Solaria's state and plan were built around agents: each agent has one state entry, one plan step and one host binding (ADR 0009). Managed Agents also has an object that belongs to the environment rather than to an agent. A Managed Agents environment is the sandbox template every session starts from (packages, networking), and on `self-hosted` it's the work queue that one worker polls. Every agent in a Solaria environment shares it. Where should that object live in the plan and the state?

## Decision drivers

- One Managed Agents environment per Solaria environment, not one per agent: a self-hosted environment needs its own worker and Console key, so one per agent multiplies both.
- A sandbox change should show in the plan once, as a change to one object.
- Destroy must delete the environment object after its agents, and apply must create it before them.
- Providers without such an object (gitagent, stub) must not change.

## Considered options

- An optional `EnvironmentHost` interface, with one host object per environment in plan and state.
- One Managed Agents environment per agent, inside each agent's entry.
- A hidden synthetic agent that stands for the environment.

## Decision outcome

Chosen option: "An optional `EnvironmentHost` interface", because it models the host as it is: one shared object, planned, applied, refreshed and destroyed like an agent, but once per environment.

- **Interface:** a provider whose host keeps such an object also implements `provider.EnvironmentHost`: `ValidateEnvironment`, `PlanEnvironment`, `ApplyEnvironment`, `ReadEnvironment` and `DestroyEnvironment`. Providers that don't implement it are unaffected.
- **Plan:** the step's ID is `Environment/<name>`. It shows above the agents, or below them for a delete, and counts like any other step. The provider records a config map, which later plans diff field by field, and a digest.
- **State:** `host` holds one entry beside `agents`, with the same provider, placement, binding, `remoteHash` and source fields. It keeps no history: the object is rebuilt from the configuration, and only agents roll back.
- **Apply order:**
  1. agent deletes;
  2. the host object's create, update or replace;
  3. the other agent steps;
  4. the host object's delete.
- **Validation:** `ValidateEnvironment` reports environment-wide problems once, on the environment's file, rather than once per agent.
- **Partial apply:** `ApplyEnvironment` may return a result together with an error when the object exists but isn't usable yet. The engine records the object and stops before the agents. A self-hosted environment does this on its first apply, while the worker waits for the Console key.
- **Lifecycle:** the host object exists while the environment has agents on that provider. Moving to another placement, or to another provider that keeps such an object, replaces it. Moving to a provider without one, or leaving no agents, deletes it.

### Consequences

- Good, because a sandbox change is one update to one object, and agents don't change when only the sandbox does.
- Good, because refresh and drift work the same way for the shared object as for agents.
- Bad, because the engine has a second kind of step, which every command that reads plans or state must handle (`status` shows it as "shared by the agents").
- Neutral: when a later host also has per-environment objects (a Foundry project, for example), it implements the same interface.

### Confirmation

Engine tests with a fake hosting provider cover the lifecycle, a partial apply and an object deleted outside Solaria. The `managed-agents` tests check the requests made in each step against golden transcripts.

## Pros and cons of the options

### One Managed Agents environment per agent

- Good, because the engine wouldn't change.
- Bad, because a self-hosted environment would need one worker and one Console key per agent.
- Bad, because a sandbox change would show, and apply, once per agent.

### A hidden synthetic agent

- Good, because it reuses the agent code paths.
- Bad, because it isn't an agent: it has no card, model or digest, and it would leak into `status`, history and every check that counts agents.

## More information

- [ADR 0006](0006-providers-and-placements.md): providers and placements.
- [ADR 0009](0009-state.md): state.
- [Managed Agents: environments](https://platform.claude.com/docs/en/managed-agents/environments)
- Accepted 2026-11-08, as implemented in M6. The state entry was described as keeping history, which it doesn't, and a move to a provider without such an object deletes it rather than replacing it; both are corrected above.
