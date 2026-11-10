# Concepts

Solaria plans and applies agent definitions to hosts, the way Terraform plans and applies infrastructure. This page explains the pieces. The decisions behind them are in the [ADRs](adr/), and every field of each resource is on its own page: [Agent](resources/agent.md), [Environment](resources/environment.md) and [McpServer](resources/mcp-server.md).

## Agents are to environments what services are to clusters

If you've deployed microservices to Kubernetes, you already know the shape of Solaria. A team runs many services, each declared in its own manifest. `kubectl apply` sends the manifests to a cluster, and the cluster runs them side by side. Solaria does the same for agents: each agent is declared in its own Agent resource, `solaria apply` sends them to an environment, and the host behind that environment runs them side by side.

<figure class="analogy"><svg viewBox="0 0 320 316" role="img" aria-labelledby="an-k8s"><title id="an-k8s">Microservices on Kubernetes: 4 manifests, sent with kubectl apply, run side by side in cluster prod.</title><text class="an-title" x="160" y="18" text-anchor="middle">Microservices on Kubernetes</text><path class="an-file" d="M122 34h18l8 8v28h-26z"/><path class="an-fold" d="M140 34v8h8"/><path class="an-file" d="M147 34h18l8 8v28h-26z"/><path class="an-fold" d="M165 34v8h8"/><path class="an-file" d="M172 34h18l8 8v28h-26z"/><path class="an-fold" d="M190 34v8h8"/><text class="an-label" x="160" y="90" text-anchor="middle">4 manifests</text><path class="an-arrow" d="M160 100v26"/><path class="an-head" d="M154.5 121l5.5 7 5.5-7"/><text class="an-cmd" x="172" y="118">kubectl apply</text><rect class="an-place" x="16" y="136" width="288" height="146" rx="6"/><text class="an-label" x="30" y="157">cluster prod</text><rect class="an-svc" x="36" y="170" width="116" height="42" rx="3"/><text class="an-name" x="94" y="195" text-anchor="middle">checkout</text><rect class="an-svc" x="168" y="170" width="116" height="42" rx="3"/><text class="an-name" x="226" y="195" text-anchor="middle">payments</text><rect class="an-svc" x="36" y="224" width="116" height="42" rx="3"/><text class="an-name" x="94" y="249" text-anchor="middle">search</text><rect class="an-svc" x="168" y="224" width="116" height="42" rx="3"/><text class="an-name" x="226" y="249" text-anchor="middle">inventory</text><text class="an-label" x="160" y="306" text-anchor="middle">the cluster runs the containers</text></svg><svg viewBox="0 0 320 316" role="img" aria-labelledby="an-sol"><title id="an-sol">Agents with Solaria: 4 Agent resources, sent with solaria apply, run side by side in environment ma-cloud.</title><text class="an-title" x="160" y="18" text-anchor="middle">Agents with Solaria</text><path class="an-file" d="M122 34h18l8 8v28h-26z"/><path class="an-fold" d="M140 34v8h8"/><path class="an-file" d="M147 34h18l8 8v28h-26z"/><path class="an-fold" d="M165 34v8h8"/><path class="an-file" d="M172 34h18l8 8v28h-26z"/><path class="an-fold" d="M190 34v8h8"/><text class="an-label" x="160" y="90" text-anchor="middle">4 Agent resources</text><path class="an-arrow" d="M160 100v26"/><path class="an-head" d="M154.5 121l5.5 7 5.5-7"/><text class="an-cmd" x="172" y="118">solaria apply</text><rect class="an-place" x="16" y="136" width="288" height="146" rx="6"/><text class="an-label" x="30" y="157">environment ma-cloud</text><rect class="an-agent" x="36" y="170" width="116" height="42" rx="3"/><text class="an-name" x="94" y="195" text-anchor="middle">reviewer</text><rect class="an-agent" x="168" y="170" width="116" height="42" rx="3"/><text class="an-name" x="226" y="195" text-anchor="middle">triage</text><rect class="an-agent" x="36" y="224" width="116" height="42" rx="3"/><text class="an-name" x="94" y="249" text-anchor="middle">docs-writer</text><rect class="an-agent" x="168" y="224" width="116" height="42" rx="3"/><text class="an-name" x="226" y="249" text-anchor="middle">release-notes</text><text class="an-label" x="160" y="306" text-anchor="middle">the host runs the agent loops</text></svg><figcaption>The same shape: many workloads, each declared in its own YAML, applied into one enclosed place that runs them.</figcaption></figure>

| Kubernetes | Solaria |
|---|---|
| A manifest for each service | An [Agent](resources/agent.md) for each agent |
| A cluster, or a namespace in one | An [Environment](resources/environment.md) |
| A Service other workloads reach by name | An [McpServer](resources/mcp-server.md) that agents refer to with `ref:` |
| The same manifests on a cluster on your laptop or on a managed one in the cloud | The same Agent on `gitagent` in Docker or on Managed Agents in the cloud |
| An overlay per cluster, such as Kustomize | `spec.overrides` on the Environment |
| An image digest | `resolvedDigest` |
| `kubectl diff`, then `kubectl apply` | `solaria plan`, then `solaria apply` |

The analogy stops at two points. Kubernetes runs a controller that reconciles all the time; Solaria reconciles only when you run `plan` and `apply`, and keeps what it deployed in a state file, the way Terraform does. And Solaria is closer to `kubectl` than to the cluster: it never runs an agent itself. The host does.

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

An environment receives every agent, or those matching `spec.agents.selector`. Moving an agent to another host means applying it to another environment. The Agent doesn't change.

## Providers and placements

A **provider** is the code that talks to one kind of host. A **placement** is where that host keeps the agent's sandbox. "Local" isn't special: it's an environment like any other.

| Provider | Placements | Agent loop | Tools run in |
|---|---|---|---|
| [`stub`](providers/stub.md) | `file` | nowhere: it writes files, for tests and examples | nowhere |
| [`gitagent`](providers/gitagent.md) | `docker` | gitagent, in a container image | the same container |
| [`claude-agent-sdk`](providers/claude-agent-sdk.md) | `process`, `docker` | the Claude Agent SDK, as a Node or Python process | the same process, on the machine that starts it or in a container image |
| [`managed-agents`](providers/managed-agents.md) | `cloud`, `self-hosted` | Claude Managed Agents | Anthropic's sandbox, or a worker container on your machine |

Each provider declares its **capabilities**: which models it runs, which MCP transports it can use, whether it takes a custom sandbox image, whether it supports fallback models and approvals, and whether destroy deletes or archives. `plan` checks each agent against them before any host call, so an agent a host can't run fails early, with the reason. Smaller gaps are warnings, for example a limit the host doesn't enforce.

Fields that only one host understands go under `providers.<name>`: on an Environment for host settings, such as `providers.gitagent.platform`, and on an Agent for host-specific agent fields. Validate warns that they make a definition less portable.

## The sandbox

`spec.sandbox` describes the machine the agent's tools run on, in a form every host can build:

- `packages`: apt, cargo, gem, go, npm and pip packages, each in its manager's own pin syntax. This is the same list Managed Agents' cloud environments take.
- `network`: `limited` (the default) or `unrestricted`, extra hosts to `allow`, and whether `packageManagers` may reach their registries.
- `resources`: memory and disk.
- `image`: a container image, or a Dockerfile, to start from instead of the Solaria sandbox base. Only container placements take it.

Container placements build images on the **Solaria sandbox base**, which follows the Managed Agents cloud sandbox: Ubuntu 24.04 with Node, Python, Go, Rust, Java, Ruby and PHP. An agent that works in a local container finds the same tools in the cloud. Docker can't enforce network host lists or disk limits, so plan warns about them.

## Identity

Every agent has three identities:

- **Logical ID**, such as `Agent/reviewer`: the kind and name in the YAML.
- **`specDigest`**: a SHA-256 of the agent as written, including the contents of its instruction and skill files. It's the same in every environment.
- **`resolvedDigest`**: the same, after the environment's overrides. It identifies what actually runs.

Neither digest includes the provider or the placement, so the same definition has the same digest on every host. Nor do they include the agent's name, so two agents written the same way share a digest. The digests use RFC 8785 canonical JSON, so reordering keys or files doesn't change them, and changing one byte of a skill does.

`solaria digest` prints them from the configuration alone, without state or host calls, so CI and other tools can compute them:

```console
$ solaria digest -e local -a hello
AGENT        SPEC DIGEST                                                              RESOLVED DIGEST
Agent/hello  sha256:08ffaed7b158236f52eb743bb34362fe8fc394e6c9399cf06e771fb2d26ffe2f  sha256:08ffaed7b158236f52eb743bb34362fe8fc394e6c9399cf06e771fb2d26ffe2f
```

`-a` picks agents and can be repeated. Without `-e`, digest prints only each `specDigest`. `-o json` gives the same in a fixed shape, with the definition format the digests were computed in.

The **host binding** maps the logical ID to the host's own identity: an image tag, or a Managed Agents agent ID and version. Providers also write the logical ID and digests onto the host object, in labels or metadata, so a trace or a console view can be traced back to the definition.

## State

`.solaria/state/<environment>.json` records, for each agent: its provider and placement, both digests, the host binding, a `remoteHash` of the host object, the git commit it was applied from, when and by whom, and a history of earlier bindings. Environments whose host keeps a shared object of its own, such as a Managed Agents environment, also have a `host` entry (ADR 0012).

State never holds secret values, only the names of secrets. It's gitignored, and a state lock (`.solaria/state/<environment>.lock`) stops two applies from running at once. Each environment has its own state, so give every developer their own environments, with a name prefix on host objects.

## The workflow

```console
$ solaria validate                # schema, references, files
$ solaria digest -e <environment> # each agent's digests, offline
$ solaria lock -e <environment>   # pin them, for review in git
$ solaria plan -e <environment>   # what apply would do
$ solaria apply -e <environment>  # do it, after confirming
$ solaria status                  # what's deployed where
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

**Drift.** Refresh asks the host for each object. An object changed outside Solaria, for example in a console, plans an update that puts the definition's version back. One deleted outside Solaria plans a create. `plan --refresh=false` skips the host calls.

**Rollback** is a checkout and an apply: `git checkout <earlier commit>` and `solaria apply`. Hosts that version agents, such as Managed Agents, create a new version with the old content, and the history in state shows that both versions share a `resolvedDigest`.

## Pinning environments

State records what is deployed. `solaria.lock` records the other half: what an environment is allowed to run. It's a JSON file next to `.solaria/`, committed to git, that pins each agent of an environment to its digests (ADR 0013):

```console
$ solaria lock -e prod
Locked Environment "prod" in solaria.lock:
  +  Agent/triage  sha256:aee6fa6c5659
```

Once an environment is in the lock, `plan` and `apply` refuse to deploy anything else to it, before any host call. They fail when an agent's `resolvedDigest` differs from its pin, when the environment selects an agent the lock doesn't list, and when the lock lists an agent the environment no longer selects. Each error ends with the command that accepts the change:

```text
solaria.lock: error: Environment "prod" is locked to Agent/triage at resolvedDigest sha256:aee6…, but the configuration resolves to sha256:9d21…; run solaria lock -e prod -a triage to promote it
```

So promoting a change is: run `solaria lock -e prod -a triage`, commit, have the diff reviewed, and apply after the merge. When and whether to promote stays with your team.

- Without `-a`, `lock` pins every agent the environment selects and drops the rest. With `-a`, it updates only those agents.
- A pin whose digests haven't changed is left as it is, so the diff shows only real changes.
- Each pin records the commit it was computed from, unless the worktree had uncommitted changes.
- `lock` reads only the configuration: no state, no host. `--lock-file` points at another file.
- Environments not in the lock aren't checked, so personal and dev environments work as before. `solaria lock -e <environment> --remove` unpins one.
- `validate` checks the file, and `status` adds a `LOCKED` column for locked environments. `destroy` ignores the lock.

A locked environment whose source has moved on keeps running the version it has until that version is promoted. To redeploy it from scratch, check out the commit its pins record.

## What Solaria doesn't do

Solaria never runs an agent loop. It defines, deploys and tracks agents; hosts run them. Promotion between environments, autonomy levels and evals belong to the team's own process, which can use Solaria's digests to tie results to definitions.
