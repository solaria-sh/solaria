# `managed-agents` provider

The `managed-agents` provider deploys agents to [Claude Managed Agents](https://platform.claude.com/docs/en/managed-agents/overview). Anthropic runs the agent loop. The tools run in Anthropic's cloud sandbox (`cloud`), or in a worker container on your machine (`self-hosted`).

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Target
metadata: {name: ma-cloud}
spec:
  provider: managed-agents
  placement: cloud
  environments: [{ref: python}]
  providers:
    managed-agents: {namePrefix: alice-}
---
apiVersion: solaria.sh/v1alpha1
kind: Environment
metadata: {name: python}
spec:
  packages: {pip: [ruff]}
  network: {mode: limited, packageManagers: true}
```

**Needs:** `ANTHROPIC_API_KEY` (or an `ant` CLI profile) for `apply`, refresh and `destroy`. `plan` needs nothing until there's state to refresh. `self-hosted` also needs Docker and the environment's key.

**Apply creates real objects** in the key's Anthropic organization. Destroy **archives** agents, which can't be undone.

## What apply does

1. **Environments.** One Managed Agents environment for each [Environment](../concepts.md#the-sandbox) the target's agents use, shared by those agents and shown in plans as `Environment/<name>` ([ADR 0012](../adr/0012-environment-level-host-objects.md)). A target without `spec.environments` gets one for the built-in sandbox, `Environment/default`. On `cloud` it holds the packages and networking; on `self-hosted` it's the queue a worker polls. One no agent uses any more is deleted.
2. **Vaults.** One host vault for each [Vault](../concepts.md#credentials-and-vaults) the target's agents reference, shown as `Vault/<name>`. See [Vaults](#vaults).
3. **Skills.** Each skill is uploaded as a custom skill. A changed skill gets a new version, and the agent pins the exact version.
4. **The agent.** Created, or updated with the version Solaria last saw, so a change made on the host in the meantime is refused rather than overwritten. Every update creates a new agent version; sessions pinned to an earlier version keep it.
5. **Cleanup.** Skills the agent no longer lists are deleted, and vaults no agent references any more.

Agents are named after their Solaria resources, behind the optional `namePrefix`: `alice-reviewer`. The target's default Environment is named after the target, `alice-ma-cloud`, and every other after the target and the Environment, `alice-ma-cloud-data`, since several targets can share an Environment. The agents' metadata holds `solaria_logical_id`, `solaria_target`, `solaria_environment`, `solaria_spec_digest` and `solaria_resolved_digest`; the environments' holds `solaria_target` and `solaria_environment`.

A session starts in the environment of the agent's Environment. `solaria status -t <target> -a <agent>` prints its ID, and `status -o json` has it as each agent's `environment`.

## How the definition maps

| Solaria | Managed Agents |
|---|---|
| `model.preferred` | `model.id`. Only `anthropic:` models; anything else fails plan. |
| `model.effort` | `model.effort` |
| `model.fallback` | not supported; plan warns |
| `instructions` | `system`, the files joined with a blank line |
| `tools.builtin` | the `agent_toolset_20260401` toolset, with every tool off except those listed. `shell` becomes `bash`; the others keep their names. |
| `tools.mcp` | `mcp_servers` (the server's streamable HTTP remote, else SSE) and one `mcp_toolset` each. On `self-hosted`, a server with only an npm or PyPI stdio package runs on the worker instead, and its tools become custom tools: see [MCP servers on the worker](#mcp-servers-on-the-worker). Other servers without a remote fail plan: Anthropic opens the connection. |
| `allowTools` | the toolset's per-tool configs, with its default off, or the custom tools declared for a server on the worker |
| `permissions.ask` | `always_ask` on that tool; every other tool is `always_allow`, because MCP toolsets would otherwise ask by default. A tool of a server on the worker fails plan: custom tools can't ask for approval. |
| `skills` | custom skills, pinned by version |
| `limits` | not enforced: a session runs until the agent finishes or is interrupted. Plan warns for each field set. Session budgets cap cost. |
| `output.schema` | not supported; plan fails |
| `workspace.access` | `read-only` fails plan: a session's files belong to the host, which can't keep them from being written. A target that binds `inputs.workspace.path` fails plan too, since runs don't work on a folder on your machine. |
| Environment `setup` | On `self-hosted`, the worker runs the steps at the start of each session: see [Setup on the worker](#setup-on-the-worker). On `cloud`, nothing of Solaria's runs before a session, so setup fails plan; attach the resources a session needs, such as a `github_repository`, when you start it. |
| `providers.managed-agents` on the Agent | sent in the request as they are, for fields Solaria has no word for, such as `multiagent`. Fields Solaria sets itself fail validation. |

Managed Agents agents hold no credentials, and the host connects to MCP servers without custom headers: a session gets credentials from the [vaults](https://platform.claude.com/docs/en/managed-agents/vaults) attached when it starts. An agent whose MCP servers send a secret header and that lists no `vaults` gets a warning. A header with a plain value is left out, with a warning.

## Vaults

Apply keeps one host vault per [Vault](../concepts.md#credentials-and-vaults) and target, named `<namePrefix><target>/<vault>`, and reads each value from the target's secret source. A credential becomes:

| Vault credential | Host credential |
|---|---|
| `for: {mcpServer: …}` | a static bearer token for the server's remote URL. The host sends `Authorization: Bearer <token>`, so the server must take the secret in its `Authorization` header, or plan fails. A leading `Bearer ` in the value is dropped, so one value works here and where a launcher sends the header as it is. A secret only a package uses isn't stored: on `cloud` it gets a warning, since Managed Agents runs no MCP packages, and for a server on the `self-hosted` worker the worker passes it to the server itself. |
| `for: {env: NAME}` | an environment variable `NAME`. The sandbox holds a placeholder, and the host puts the value into outbound requests to any host the agent's Environment allows in `spec.network`. |

Sessions attach the vaults when they start; Solaria doesn't start sessions. `solaria status -t <target> -a <agent>` lists the vault IDs, and `status -o json` has them as each agent's `vaults`.

State and plans hold credential names, secret names and targets, never values, so Solaria can't tell when a value changes. Apply sends a value when its credential is created, or when its secret or target changes. After changing a value in the secret source, run `solaria apply --rotate-secrets` to send every value again; `plan --rotate-secrets` shows what that does. Every value is read before the first host call, so a missing secret changes nothing.

## Sandbox on `cloud`

An Environment's `spec` becomes the cloud environment's config:

| Solaria | Cloud environment |
|---|---|
| `packages` | `packages`, in each manager's own pin syntax |
| `network.mode` | `networking.type`. An omitted `network` is `limited`, never the host's `unrestricted` default. |
| `network.allow` | `networking.allowed_hosts`. Under `limited` it also restricts `web_fetch` and `web_search`; plan warns when an agent uses them. |
| `network.packageManagers` | `networking.allow_package_managers`. Packages under `limited` need it, or plan fails. |
| the agents' MCP servers | `networking.allow_mcp_servers`, set when any agent in the Environment uses one |
| `resources` | The cloud sandbox has fixed limits, 8 GiB memory and 10 GiB disk. A larger value fails plan; others are ignored with a warning. |
| `image` | not supported; plan fails |
| `telemetry` | ignored with a warning: sessions are traced in the Claude Console |
| identity | in the agent's metadata only. Managed Agents environments don't take variables, so runs don't get `SOLARIA_*`. |

## `self-hosted`

The agent loop stays on Anthropic's side; a worker container on your machine runs the tools. Anthropic opens the connections to MCP servers with a remote; the worker runs those with only a stdio package (see [MCP servers on the worker](#mcp-servers-on-the-worker)).

Each Environment gets its own environment, key and worker:

1. The **first apply** creates the environment and stops, explaining how to generate its key in the Claude Console. Only the Console can generate it.
2. Store the key in the target's secret source and **apply again**. Solaria builds the worker image and starts `solaria-<name>-worker` with `--restart unless-stopped`, where `<name>` is the target for its default Environment and `<target>-<environment>` for the others.

The default Environment's key is `ANTHROPIC_ENVIRONMENT_KEY`, or the name in `environmentKeySecret`. Every other Environment's adds its name in upper case, with `_` for `-`: `ANTHROPIC_ENVIRONMENT_KEY_DATA` for Environment `data`.

The worker is a small Go program on the SDK's `EnvironmentWorker`, built into an image on the [Solaria sandbox base](../concepts.md#the-sandbox) with the Environment's packages, tagged `solaria/<name>/worker:<digest>`. The key reaches docker through its environment, never its command line, and the container never holds an API key. `resources.memory` becomes the container's memory limit. `image` is accepted: the worker image starts from it, and it needs `/bin/bash`. Docker can't enforce network host lists or disk limits, so plan warns about them.

A plan that finds the worker container stopped plans an update, which starts it again.

### MCP servers on the worker

Anthropic can't start a stdio server, so on `self-hosted` the worker runs every MCP server that has no remote and has an npm or PyPI package with `transport: {type: stdio}`, and serves its tools to the agents as [custom tools](https://platform.claude.com/docs/en/managed-agents/self-hosted-sandboxes-custom-tools) ([ADR 0021](../adr/0021-self-hosted-worker-runs-stdio-mcp-servers.md)). A server with a remote still uses it. Plan notes the servers that run on the worker.

- **Install.** The worker image installs each server at the version its package pins: npm packages with `npm install --prefix /opt/solaria/mcp/<server>`, PyPI packages into a virtual environment there. A new version or argument changes the image, so apply rebuilds it and restarts the worker. With `image`, the image needs `npm` or `python3` with `venv`; plan warns.
- **Tools.** Apply lists each server's tools once, after building the image, by running `solaria-worker list-tools` in it, and records them in state. The agent declares them as custom tools named `<server>__<tool>`, narrowed by `allowTools`; later plans render from state and show changes as field diffs. Until a server has been listed, plan says apply will list it. A server that fails to start or list fails apply before any agent changes. A server whose tools change at runtime keeps the tools listed at apply until the next one.
- **Limits.** A tool name longer than 128 characters with its prefix, an input schema with `$ref` or a top-level `oneOf`, `anyOf` or `allOf`, a property name other than letters, digits, `_`, `.` and `-`, or more than 256 tools on one agent fails apply, naming the server and tool.
- **Calls.** Each session starts its own server process the first time it calls one of the server's tools, in the session's workdir, and stops it when the session ends. A call times out after 10 minutes and becomes an error result.
- **Secrets.** A package variable with `secretRef` reaches the worker container from the target's secret source through docker's environment, as the key does, and the worker passes each server only its own variables, never the key. The agent's `bash` gets the container's environment without the key, any server's secrets or any setup step's. Every agent that uses a server is granted all its secrets, straight or through its vaults, so one worker serving several agents gives the server the same values.
- **Approvals.** The worker runs every custom tool call, so `permissions.ask` on a tool of a server on the worker fails plan. Use a server with a remote for tools that need approval.

The server's tool names, descriptions and results reach the model, and the worker runs `bash` next to it: run only servers you trust.

### Setup on the worker

The worker runs its Environment's [setup steps](../concepts.md#setup-steps) at the start of each session, before it serves the session's tools, each with `sh -e` in `/workspace` ([ADR 0022](../adr/0022-environment-setup-steps.md)). A worker serves one target and Environment, so apply builds the steps and the target's values into the image, in `/etc/solaria/worker.json` next to the MCP servers.

- **The workdir is shared.** Every session on a worker works in the same `/workspace`, where the SDK also keeps each session's `skills/` and `memory/` folders, so the worker doesn't empty it. A step must cope with what an earlier session left.
- **Secrets.** A step's `secretRef` reaches the container through docker's environment, as the servers' secrets do. The step gets `PATH`, `HOME` and its own variables; the agent's `bash` and the MCP servers never see a step's secrets.
- **Failure.** A step that exits non-zero is logged to the container's output, and every tool call of that session returns the failure, so the agent can't work on a workspace that wasn't prepared. The next session runs setup again.

## Settings

| Setting | Default | |
|---|---|---|
| `providers.managed-agents.namePrefix` | none | Put before every host object's name, so personal targets don't collide. |
| `providers.managed-agents.environmentKeySecret` | `ANTHROPIC_ENVIRONMENT_KEY` | `self-hosted` only: the secret holding the default Environment's key. Other Environments' add `_<NAME>`. |
| `providers.managed-agents.platform` | `linux/amd64` | `self-hosted` only: the worker image's platform, `linux/amd64` or `linux/arm64`. |

Managed Agents runs the agent loop itself, and its runtime can't be pinned to a version: two runs of the same agent version months apart may use different harnesses. The binding records the toolset an agent uses, `agent_toolset_20260401`, as its runtime, which `status -a` shows. An agent without built-in tools records none.

## Drift, rollback and destroy

- **Drift:** the host increases an agent's version on every change, and an environment's `updated_at`. Refresh compares them with the state, so any change made in the Console shows in the plan, and apply puts the definition's version back. For a vault, refresh compares its credentials' IDs and update times: a credential added, removed or changed in the Console makes the vault update, and a deleted vault is created again.
- **Rollback:** Managed Agents can't point an agent back at an old version. Applying old content creates a new version with the old `resolvedDigest`, and the state's history shows both. Sessions can pin an earlier version directly.
- **Destroy** archives each agent, deletes its skills and vaults, stops and removes a self-hosted worker and its images, and deletes the environments. An environment that sessions still refer to is archived instead.

## Testing

The provider's tests run against an in-memory fake of the API and compare every request with golden transcripts in `internal/providers/managedagents/testdata/`. `SOLARIA_IT_MANAGED_AGENTS=1` runs an integration test against the real API, under names starting with `solaria-it-`.
