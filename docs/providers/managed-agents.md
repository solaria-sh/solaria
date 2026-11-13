# `managed-agents` provider

The `managed-agents` provider deploys agents to [Claude Managed Agents](https://platform.claude.com/docs/en/managed-agents/overview). Anthropic runs the agent loop. The tools run in Anthropic's cloud sandbox (`cloud`), or in a worker container on your machine (`self-hosted`).

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Target
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

1. **The environment.** One Managed Agents environment per Solaria target, shared by all its agents and shown in plans as `Environment/<name>` ([ADR 0012](../adr/0012-environment-level-host-objects.md)). On `cloud` it holds the packages and networking; on `self-hosted` it's the queue a worker polls.
2. **Vaults.** One host vault for each [Vault](../concepts.md#credentials-and-vaults) the target's agents reference, shown as `Vault/<name>`. See [Vaults](#vaults).
3. **Skills.** Each skill is uploaded as a custom skill. A changed skill gets a new version, and the agent pins the exact version.
4. **The agent.** Created, or updated with the version Solaria last saw, so a change made on the host in the meantime is refused rather than overwritten. Every update creates a new agent version; sessions pinned to an earlier version keep it.
5. **Cleanup.** Skills the agent no longer lists are deleted, and vaults no agent references any more.

Agents are named after their Solaria resources, and the environment after its target, behind the optional `namePrefix`: `alice-reviewer`, `alice-ma-cloud`. Their metadata holds `solaria_logical_id`, `solaria_target`, `solaria_spec_digest` and `solaria_resolved_digest`.

## How the definition maps

| Solaria | Managed Agents |
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
| `output.schema` | not supported; plan fails |
| `workspace.access` | `read-only` fails plan: a session's files belong to the host, which can't keep them from being written. A target that binds `inputs.workspace.path` fails plan too, since runs don't work on a folder on your machine. |
| `repositories` | On `cloud`, each GitHub repository is a `github_repository` resource the caller attaches when starting a session: `status -a` shows its URL, mount path (`/workspace/repositories/<name>`) and the secret holding its token, its `authorization_token`. `ref` becomes its branch checkout, and `depth` is ignored with a warning. A repository on another git host fails plan, and so does a read-only one: the session can write its clone. On `self-hosted`, targets take no resources, so repositories fail plan. |
| `providers.managed-agents` on the Agent | sent in the request as they are, for fields Solaria has no word for, such as `multiagent`. Fields Solaria sets itself fail validation. |

Managed Agents agents hold no credentials, and the host connects to MCP servers without custom headers: a session gets credentials from the [vaults](https://platform.claude.com/docs/en/managed-agents/vaults) attached when it starts. An agent whose MCP servers send a secret header and that lists no `vaults` gets a warning. A header with a plain value is left out, with a warning.

## Vaults

Apply keeps one host vault per [Vault](../concepts.md#credentials-and-vaults) and target, named `<namePrefix><target>/<vault>`, and reads each value from the target's secret source. A credential becomes:

| Vault credential | Host credential |
|---|---|
| `for: {mcpServer: …}` | a static bearer token for the server's remote URL. The host sends `Authorization: Bearer <token>`, so the server must take the secret in its `Authorization` header, or plan fails. A leading `Bearer ` in the value is dropped, so one value works here and where a launcher sends the header as it is. A secret only a package uses isn't stored, with a warning: Managed Agents runs no MCP packages. |
| `for: {env: NAME}` | an environment variable `NAME`. The sandbox holds a placeholder, and the host puts the value into outbound requests to any host the target's `sandbox.network` allows. |
| `for: {gitHost: …}` | nothing: it authenticates a repository's clone, and the caller passes its value as the `github_repository` resource's token. |

Sessions attach the vaults when they start; Solaria doesn't start sessions. `solaria status -t <target> -a <agent>` lists the vault IDs, and `status -o json` has them as each agent's `vaults`.

State and plans hold credential names, secret names and targets, never values, so Solaria can't tell when a value changes. Apply sends a value when its credential is created, or when its secret or target changes. After changing a value in the secret source, run `solaria apply --rotate-secrets` to send every value again; `plan --rotate-secrets` shows what that does. Every value is read before the first host call, so a missing secret changes nothing.

## Sandbox on `cloud`

| Solaria | Cloud environment |
|---|---|
| `packages` | `packages`, in each manager's own pin syntax |
| `network.mode` | `networking.type`. An omitted `network` is `limited`, never the host's `unrestricted` default. |
| `network.allow` | `networking.allowed_hosts`. Under `limited` it also restricts `web_fetch` and `web_search`; plan warns when an agent uses them. |
| `network.packageManagers` | `networking.allow_package_managers`. Packages under `limited` need it, or plan fails. |
| the agents' MCP servers | `networking.allow_mcp_servers`, set when any agent uses one |
| `resources` | The cloud sandbox has fixed limits, 8 GiB memory and 10 GiB disk. A larger value fails plan; others are ignored with a warning. |
| `image` | not supported; plan fails |
| `telemetry` | ignored with a warning: sessions are traced in the Claude Console |
| identity | in the agent's metadata only. Managed Agents environments don't take variables, so runs don't get `SOLARIA_*`. |

## `self-hosted`

The agent loop stays on Anthropic's side; a worker container on your machine runs the tools. Anthropic still opens the MCP connections.

1. The **first apply** creates the environment and stops, explaining how to generate its key in the Claude Console. Only the Console can generate it.
2. Store the key in the target's secret source as `ANTHROPIC_ENVIRONMENT_KEY` (or the name in `environmentKeySecret`) and **apply again**. Solaria builds the worker image and starts `solaria-<target>-worker` with `--restart unless-stopped`.

The worker is a small Go program on the SDK's `EnvironmentWorker`, built into an image on the [Solaria sandbox base](../concepts.md#the-sandbox) with the target's packages, tagged `solaria/<target>/worker:<digest>`. The key reaches docker through its environment, never its command line, and the container never holds an API key. `resources.memory` becomes the container's memory limit. `image` is accepted: the worker image starts from it, and it needs `/bin/bash`. Docker can't enforce network host lists or disk limits, so plan warns about them.

A plan that finds the worker container stopped plans an update, which starts it again.

## Settings

| Setting | Default | |
|---|---|---|
| `providers.managed-agents.namePrefix` | none | Put before every host object's name, so personal targets don't collide. |
| `providers.managed-agents.environmentKeySecret` | `ANTHROPIC_ENVIRONMENT_KEY` | `self-hosted` only: the secret holding the environment's key. |
| `providers.managed-agents.platform` | `linux/amd64` | `self-hosted` only: the worker image's platform, `linux/amd64` or `linux/arm64`. |

Managed Agents runs the agent loop itself, and its runtime can't be pinned to a version: two runs of the same agent version months apart may use different harnesses. The binding records the toolset an agent uses, `agent_toolset_20260401`, as its runtime, which `status -a` shows. An agent without built-in tools records none.

## Drift, rollback and destroy

- **Drift:** the host increases an agent's version on every change, and an environment's `updated_at`. Refresh compares them with the state, so any change made in the Console shows in the plan, and apply puts the definition's version back. For a vault, refresh compares its credentials' IDs and update times: a credential added, removed or changed in the Console makes the vault update, and a deleted vault is created again.
- **Rollback:** Managed Agents can't point an agent back at an old version. Applying old content creates a new version with the old `resolvedDigest`, and the state's history shows both. Sessions can pin an earlier version directly.
- **Destroy** archives each agent, deletes its skills and vaults, stops and removes a self-hosted worker and its images, and deletes the environment. An environment that sessions still refer to is archived instead.

## Testing

The provider's tests run against an in-memory fake of the API and compare every request with golden transcripts in `internal/providers/managedagents/testdata/`. `SOLARIA_IT_MANAGED_AGENTS=1` runs an integration test against the real API, under names starting with `solaria-it-`.
