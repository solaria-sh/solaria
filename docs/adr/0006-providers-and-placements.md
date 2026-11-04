---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# Providers and placements; "local" is an environment

## Context and problem statement

Agent hosts come in two kinds:

- **Config-hosted:** the host runs the loop from a definition (Claude Managed Agents).
- **Container-hosted:** you bring the loop in a container (Microsoft Foundry hosted agents, Kubernetes, a local Docker container).

People also want several ways to run "locally", for example gitagent in a container, or a Managed Agents self-hosted worker in a container, to compare them before choosing a host. How should a provider be defined?

## Decision drivers

- Compare runtimes side by side on one machine.
- The provider interface stays small.
- Host limits show up at plan time.

## Considered options

- One `local` provider.
- One provider per host and runtime, with a placement for where the sandbox lives.

## Decision outcome

Chosen option: "Provider plus placement". A **provider** is whoever runs the agent loop. A **placement** is where its sandbox lives. "Local" is not a provider; it's an **environment** that binds agents to a provider and placement:

```yaml
kind: Environment
metadata: { name: local-gitagent }
spec: { provider: gitagent, placement: docker, … }
```

v0 providers:

| Provider | Runs the loop | Placements |
|---|---|---|
| `gitagent` | gitagent, in a container Solaria builds | `docker` |
| `managed-agents` | Anthropic | `cloud`, `self-hosted` |

On `managed-agents/self-hosted`, `apply` owns the worker container: a long-running poller using `ant beta:worker poll` or the SDK's `EnvironmentWorker`. That's infrastructure, not a session, so it doesn't conflict with ADR 0002. The Go SDK ships `EnvironmentWorker` (`lib/environments`, v2.5.0), so the worker can be a small Go program built by Solaria, with no `ant` CLI in the image.

**How `gitagent` renders** (verified in the [gitagent spike](../spikes/2026-11-gitagent-v3.md)):

- The definition is written to gitagent's config layer in the image's `$HOME/.gitagent/`:
  - `gitagent.yaml`: model, limits, `mcp_servers`;
  - `prompts/system.md`, `prompts/rules.md`;
  - `skills/`.
- It is not written into the workspace, and gitagent is run in the workspace **without `--dir`**. Otherwise it scaffolds and commits agent files into the user's repository.
- Tools are always passed as an explicit `--allowed-tools` list, because the manifest's `tools:` doesn't restrict built-ins.
- `permissions.ask` becomes `--disallowed-tools` in unattended runs. gitagent denies approvals when no one is at a terminal, and `compliance` isn't read from the config layer.
- Until gitagent v3 is released, the image builds gitagent from a pinned commit.

### Provider interface

It's modeled on Terraform's resource lifecycle:

```go
type Provider interface {
    Capabilities(placement string) Capabilities
    Validate(ctx, ResolvedAgent, Environment) []Diagnostic
    Plan(ctx, desired ResolvedAgent, prior *StateEntry) (Plan, error)
    Apply(ctx, Plan) (StateEntry, error)
    Read(ctx, StateEntry) (Observed, error)
    Destroy(ctx, StateEntry) error
}
```

`Capabilities` declares, for each placement:

- which models are allowed (Managed Agents accepts only Claude models);
- the MCP transports;
- the sandbox forms;
- whether fallback models are supported;
- which permission features exist.

The engine checks these at plan time, before any host call. `Import` follows later. In v0, providers are compiled in and live under `internal/`. Out-of-process plugins come later.

### Consequences

- Good, because the same card can run under several runtimes on one machine for comparison.
- Good, because a container-hosted placement built now (Docker) is the starting point for Kubernetes and Foundry later.
- Bad, because a provider's own requirements leak into setup. For example, Managed Agents self-hosted needs an environment key generated in the Console after the environment exists. The first `apply` stops and says exactly what to do.

### Confirmation

Every provider has capability tests, and golden tests of what it sends to its host.

## More information

- [Managed Agents: self-hosted sandboxes](https://platform.claude.com/docs/en/managed-agents/self-hosted-sandboxes), [deploying workers](https://platform.claude.com/docs/en/managed-agents/self-hosted-sandboxes-workers)
- [Foundry hosted agents](https://learn.microsoft.com/en-us/azure/foundry/agents/concepts/hosted-agents)
- [gitagent](https://github.com/open-gitagent/gitagent)
- Revised 2026-11-04 after the M1 spikes: gitagent rendering, Go SDK worker. The `managed-agents` implementation approach is in ADR 0011.
