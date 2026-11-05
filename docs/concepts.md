# Concepts

Solaria plans and applies agent definitions to hosts, the way Terraform plans and applies infrastructure. This page explains the pieces. The decisions behind them are in the [ADRs](adr/), and every field is in the [card reference](reference.md).

## Resources

A Solaria project is a folder of YAML files. Each document in them is a resource with `apiVersion: solaria.sh/v1alpha1`, a `kind`, `metadata.name` and a `spec`. Solaria reads every `.yaml` and `.yml` file under the folder, except in hidden folders, `node_modules`, `vendor` and `testdata`.

There are three kinds:

- **Agent**: what the agent is, independently of any host. Its instructions (Markdown files), model, built-in tools, MCP servers, [Agent Skills](https://agentskills.io), permissions and limits.
- **McpServer**: an MCP server, in the shape of the [MCP Registry's `server.json`](https://github.com/modelcontextprotocol/registry): `packages` that run locally over stdio (npm, PyPI), `remotes` reached over HTTP, or both. Agents refer to servers by name with `ref:`, and can narrow them with `allowTools`.
- **Environment**: where agents run. It names a provider and a placement, and holds everything about the place rather than the agent: the sandbox, the secret source, telemetry, per-agent overrides and host settings.

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Environment
metadata: {name: ma-cloud}
spec:
  provider: managed-agents
  placement: cloud
  sandbox:
    packages: {apt: [ripgrep]}
    network: {mode: limited, allow: [github.com], packageManagers: true}
  overrides:
    reviewer:
      model: {preferred: anthropic:claude-haiku-5-5}
```

An environment receives every agent, or those matching `spec.agents.selector`. Moving an agent to another host means applying it to another environment. The card doesn't change.

## Providers and placements

A **provider** is the code that talks to one kind of host. A **placement** is where that host keeps the agent's sandbox. "Local" isn't special: it's an environment like any other.

| Provider | Placements | Agent loop | Tools run in |
|---|---|---|---|
| [`stub`](providers/stub.md) | `file` | nowhere: it writes files, for tests and examples | nowhere |
| [`gitagent`](providers/gitagent.md) | `docker` | gitagent, in a container image | the same container |
| [`managed-agents`](providers/managed-agents.md) | `cloud`, `self-hosted` | Claude Managed Agents | Anthropic's sandbox, or a worker container on your machine |

Each provider declares its **capabilities**: which models it runs, which MCP transports it can use, whether it takes a custom sandbox image, whether it supports fallback models and approvals, and whether destroy deletes or archives. `plan` checks the card against them before any host call, so a card a host can't run fails early, with the reason. Smaller gaps are warnings, for example a limit the host doesn't enforce.

Fields that only one host understands go under `providers.<name>`: on an Environment for host settings, such as `providers.gitagent.platform`, and on an Agent for host-specific agent fields. Validate warns that they make a card less portable.

## The sandbox

`spec.sandbox` describes the machine the agent's tools run on, in a form every host can build:

- `packages`: apt, cargo, gem, go, npm and pip packages, each in its manager's own pin syntax. This is the same list Managed Agents' cloud environments take.
- `network`: `limited` (the default) or `unrestricted`, extra hosts to `allow`, and whether `packageManagers` may reach their registries.
- `resources`: memory and disk.
- `image`: a container image, or a Dockerfile, to start from instead of the Solaria sandbox base. Only container placements take it.

Container placements build images on the **Solaria sandbox base**, which follows the Managed Agents cloud sandbox: Ubuntu 24.04 with Node, Python, Go, Rust, Java, Ruby and PHP. An agent that works in a local container finds the same tools in the cloud. Docker can't enforce network host lists or disk limits, so plan warns about them.

## Identity

Every agent has three identities:

- **Logical ID**, such as `Agent/reviewer`: the name in the card.
- **`specDigest`**: a SHA-256 of the agent as written, including the contents of its instruction and skill files. It's the same in every environment.
- **`resolvedDigest`**: the same, after the environment's overrides. It identifies what actually runs.

Neither digest includes the provider or the placement, so the same definition has the same digest on every host. The digests use RFC 8785 canonical JSON, so reordering keys or files doesn't change them, and changing one byte of a skill does.

The **host binding** maps the logical ID to the host's own identity: an image tag, or a Managed Agents agent ID and version. Providers also write the logical ID and digests onto the host object, in labels or metadata, so a trace or a console view can be traced back to the card.

## State

`.solaria/state/<environment>.json` records, for each agent: its provider and placement, both digests, the host binding, a `remoteHash` of the host object, the git commit it was applied from, when and by whom, and a history of earlier bindings. Environments whose host keeps a shared object of its own, such as a Managed Agents environment, also have a `host` entry (ADR 0012).

State never holds secret values, only the names of secrets. It's gitignored, and a lock file stops two applies from running at once. Each environment has its own state, so give every developer their own environments, with a name prefix on host objects.

## The workflow

```console
$ solaria validate               # schema, references, files
$ solaria plan -e <environment>  # what apply would do
$ solaria apply -e <environment> # do it, after confirming
$ solaria status                 # what's deployed where
$ solaria destroy -e <environment>
```

`plan` loads the files, resolves each agent in the environment, checks capabilities, refreshes the state from the host, and compares. It shows each change by field, and the new `resolvedDigest`:

```text
Environment ma-cloud (managed-agents/cloud)

  ~ Agent/reviewer
      ~ instructions[1]: text changed (5 lines -> 6 lines)
      resolvedDigest: sha256:ff171fe9c371 -> sha256:6c03b2a1e8d4
      note: creates a new agent version; sessions pinned to an earlier one keep it

Plan: 0 to create, 1 to update, 0 to replace, 0 to delete or archive.
```

Symbols: `+` create, `~` update, `-/+` replace (the agent moved to another provider or placement), `-` delete, and `-/archive` for hosts that can only archive.

**Drift.** Refresh asks the host for each object. An object changed outside Solaria, for example in a console, plans an update that puts the card's version back. One deleted outside Solaria plans a create. `plan --refresh=false` skips the host calls.

**Rollback** is a checkout and an apply: `git checkout <earlier commit>` and `solaria apply`. Hosts that version agents, such as Managed Agents, create a new version with the old content, and the history in state shows that both versions share a `resolvedDigest`.

## What Solaria doesn't do

Solaria never runs an agent loop. It defines, deploys and tracks agents; hosts run them. Promotion between environments, autonomy levels and evals belong to the team's own process, which can use Solaria's digests to tie results to definitions.
