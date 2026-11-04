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

On `managed-agents/self-hosted`, `apply` owns the worker container: a long-running poller using `ant beta:worker poll` or the SDK's `EnvironmentWorker`. That's infrastructure, not a session, so it doesn't conflict with ADR 0002.

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
