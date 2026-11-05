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

**As implemented in M4** (`internal/provider`):

- **Identity methods:** the interface also has `Name`, `Version` (recorded in state) and `Placements`.
- **Plan:** providers build on `provider.DefaultPlan`, which computes create, update or no-op and the field diff. A provider then adjusts the result, for example turning an update into a replace or adding notes.
- **Apply** returns the host binding and a `remoteHash`. The engine writes the state entry itself: digests, source commit, history.
- **Engine-level replace:** when an agent's environment moves to another provider or placement, the engine plans a replace. It destroys the object with the old provider, then applies with the new one.
- **Test provider `stub`:** its placement `file` keeps host objects as JSON files under `.solaria/stub/`. It's the null provider of the plan, renamed because YAML reads an unquoted `null` as no value.

**`gitagent` as implemented in M5** (`internal/providers/gitagent`). It refines the spike's plan in these ways:

- **Instructions go to `prompts/rules.md`, not `prompts/system.md`.** gitagent's own system prompt explains its tools and how to finish a task (`task_complete`). Replacing it would break unattended runs, so the card's instructions follow it.
- **Tool lists:**
  - gitagent matches tool names exactly, and MCP tools are named `<server>__<tool>`, which aren't known before connecting.
  - When every MCP ref lists `allowTools`, the entrypoint gets `--allowed-tools`.
  - When none do, it gets `--disallowed-tools` for the built-ins the card leaves out.
  - A mix can't be expressed, so plan fails.
  - Tools under `permissions.ask` are removed or denied.
  - `glob`, `grep`, `web_fetch` and `web_search` have no gitagent tool, so plan warns.
- **Secrets:** a `secretRef` becomes `${NAME}` in `gitagent.yaml`, without the `SOLARIA_SECRET_` prefix the spike suggested. The run command then passes the same names that are in the secret source: `--env NAME` for `env`, `--env-file` for `dotenv`.
- **MCP transport:** a remote is used when the server has one, so the agent talks to the same server it would on a cloud host. Otherwise `npx`/`uvx` runs the package.
- **Images:**
  - `solaria/<env>/<agent>:<resolvedDigest[:12]>`, with the environment in the name, so two environments never share a tag.
  - The host ID is the repository and the version is the image ID, so a rebuild or retag outside Solaria is drift.
  - The previous tag is removed after a successful build.
- **Platform:** `linux/amd64` by default, as in the cloud sandbox; `providers.gitagent.platform: linux/arm64` builds natively on Arm machines. gitagent compiles on the build machine's own architecture and cross-compiles when the target differs, so an amd64 image on an Arm Mac doesn't compile Rust under emulation.
- **The image runs as root** with `HOME=/root`, as verified in the spike. A non-root user with the caller's UID comes later.
- **Apply prints the run command:** `docker run … <image>`, followed by `-p "<task>"`. It mounts the current folder as `/workspace` and names API keys and secrets without their values. The build context stays under `.solaria/gitagent/<env>/<agent>/` for inspection.

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
- Revised in M4: the interface as implemented, and the `stub` test provider.
- Revised in M5: `gitagent` as implemented.
