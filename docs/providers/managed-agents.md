# `managed-agents` provider

The `managed-agents` provider deploys agents to [Claude Managed Agents](https://platform.claude.com/docs/en/managed-agents/overview). Anthropic runs the agent loop. The tools run in Anthropic's cloud sandbox (`cloud`), or in a worker container on your machine (`self-hosted`).

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Environment
metadata: {name: ma-cloud}
spec:
  provider: managed-agents
  placement: cloud
  sandbox:
    packages: {pip: [ruff]}
    network: {mode: limited, packageManagers: true}
  providers:
    managed-agents: {namePrefix: alice-}
```

**Needs:** `ANTHROPIC_API_KEY` (or an `ant` CLI profile) for `apply`, refresh and `destroy`. `plan` needs nothing until there's state to refresh. `self-hosted` also needs Docker and the environment's key.

**Apply creates real objects** in the key's Anthropic organization. Destroy **archives** agents, which can't be undone.

## What apply does

1. **The environment.** One Managed Agents environment per Solaria environment, shared by all its agents and shown in plans as `Environment/<name>` ([ADR 0012](../adr/0012-environment-level-host-objects.md)). On `cloud` it holds the packages and networking; on `self-hosted` it's the queue a worker polls.
2. **Skills.** Each skill is uploaded as a custom skill. A changed skill gets a new version, and the agent pins the exact version.
3. **The agent.** Created, or updated with the version Solaria last saw, so a change made on the host in the meantime is refused rather than overwritten. Every update creates a new agent version; sessions pinned to an earlier version keep it.
4. **Cleanup.** Skills the card dropped are deleted.

Agents and the environment are named after the card, behind the optional `namePrefix`: `alice-reviewer`, `alice-ma-cloud`. Their metadata holds `solaria_logical_id`, `solaria_environment`, `solaria_spec_digest` and `solaria_resolved_digest`.

## How the card maps

| Card | Managed Agents |
|---|---|
| `model.preferred` | `model.id`. Only `anthropic:` models; anything else fails plan. |
| `model.effort` | `model.effort` |
| `model.fallback` | not supported; plan warns |
| `instructions` | `system`, the files joined with a blank line |
| `tools.builtin` | the `agent_toolset_20260401` toolset, with every tool off except those listed. `shell` becomes `bash`; the others keep their names. |
| `tools.mcp` | `mcp_servers` (the server's streamable HTTP remote, else SSE) and one `mcp_toolset` each. Servers without a remote fail plan: Anthropic opens the connection, on both placements. |
| `allowTools` | the toolset's per-tool configs, with its default off |
| `permissions.ask` | `always_ask` on that tool; every other tool is `always_allow`, because MCP toolsets would otherwise ask by default |
| `skills` | custom skills, pinned by version |
| `limits` | not enforced; plan warns. Session budgets cap cost. |
| `providers.managed-agents` on the Agent | sent in the request as they are, for fields the card has no word for, such as `multiagent`. Fields Solaria sets from the card fail validation. |

MCP servers that need headers, such as an `Authorization` token, get a warning: Managed Agents agents hold no credentials. Give sessions a [vault](https://platform.claude.com/docs/en/managed-agents/vaults) with a credential for the server's URL.

## Sandbox on `cloud`

| Card | Cloud environment |
|---|---|
| `packages` | `packages`, in each manager's own pin syntax |
| `network.mode` | `networking.type`. An omitted `network` is `limited`, never the host's `unrestricted` default. |
| `network.allow` | `networking.allowed_hosts`. Under `limited` it also restricts `web_fetch` and `web_search`; plan warns when an agent uses them. |
| `network.packageManagers` | `networking.allow_package_managers`. Packages under `limited` need it, or plan fails. |
| the agents' MCP servers | `networking.allow_mcp_servers`, set when any agent uses one |
| `resources` | The cloud sandbox has fixed limits, 8 GiB memory and 10 GiB disk. A larger value fails plan; others are ignored with a warning. |
| `image` | not supported; plan fails |
| `telemetry` | ignored with a warning: sessions are traced in the Claude Console |

## `self-hosted`

The agent loop stays on Anthropic's side; a worker container on your machine runs the tools. Anthropic still opens the MCP connections.

1. The **first apply** creates the environment and stops, explaining how to generate its key in the Claude Console. Only the Console can generate it.
2. Store the key in the environment's secret source as `ANTHROPIC_ENVIRONMENT_KEY` (or the name in `environmentKeySecret`) and **apply again**. Solaria builds the worker image and starts `solaria-<environment>-worker` with `--restart unless-stopped`.

The worker is a small Go program on the SDK's `EnvironmentWorker`, built into an image on the [Solaria sandbox base](../concepts.md#the-sandbox) with the environment's packages, tagged `solaria/<environment>/worker:<digest>`. The key reaches docker through its environment, never its command line, and the container never holds an API key. `resources.memory` becomes the container's memory limit. `image` is accepted: the worker image starts from it, and it needs `/bin/bash`. Docker can't enforce network host lists or disk limits, so plan warns about them.

A plan that finds the worker container stopped plans an update, which starts it again.

## Settings

| Setting | Default | |
|---|---|---|
| `providers.managed-agents.namePrefix` | none | Put before every host object's name, so personal environments don't collide. |
| `providers.managed-agents.environmentKeySecret` | `ANTHROPIC_ENVIRONMENT_KEY` | `self-hosted` only: the secret holding the environment's key. |
| `providers.managed-agents.platform` | `linux/amd64` | `self-hosted` only: the worker image's platform, `linux/amd64` or `linux/arm64`. |

## Drift, rollback and destroy

- **Drift:** the host increases an agent's version on every change, and an environment's `updated_at`. Refresh compares them with the state, so any change made in the Console shows in the plan, and apply puts the card's version back.
- **Rollback:** Managed Agents can't point an agent back at an old version. Applying old content creates a new version with the old `resolvedDigest`, and the state's history shows both. Sessions can pin an earlier version directly.
- **Destroy** archives each agent, deletes its skills, stops and removes a self-hosted worker and its images, and deletes the environment. An environment that sessions still refer to is archived instead.

## Testing

The provider's tests run against an in-memory fake of the API and compare every request with golden transcripts in `internal/providers/managedagents/testdata/`. `SOLARIA_IT_MANAGED_AGENTS=1` runs an integration test against the real API, under names starting with `solaria-it-`.
