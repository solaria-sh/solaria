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

There are four kinds:

- **Agent**: what the agent is, independently of any host. Its instructions (Markdown files), model, built-in tools, MCP servers, [Agent Skills](https://agentskills.io), permissions and limits, and optionally the JSON its answer must match (see [Structured answers](#structured-answers)).
- **McpServer**: an MCP server, in the shape of the [MCP Registry's `server.json`](https://github.com/modelcontextprotocol/registry): `packages` that run locally over stdio (npm, PyPI), `remotes` reached over HTTP, or both. Agents refer to servers by name with `ref:`, and can narrow them with `allowTools`.
- **Environment**: where agents run. It names a provider and a placement, and holds everything about the place rather than the agent: the sandbox, the secret source, telemetry, per-agent overrides and host settings.
- **Vault**: credentials an agent may use, each naming a secret and what it unlocks. Agents list the vaults they use in `spec.vaults`. See [Credentials and vaults](#credentials-and-vaults).

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

Besides `name`, `metadata` takes `labels`, for selectors, and `annotations`, for notes about how a resource is operated rather than what it is: an agent's owner, its trigger, the gate it passes before promotion, the metric it's judged by. Annotation values are any text. Solaria never reads them: they're left out of the digests and never sent to a host, so changing one changes nothing deployed. `solaria status -o json` shows each agent's.

```yaml
metadata:
  name: reviewer
  labels: {team: platform}
  annotations:
    owner: platform-team
    trigger: every pull request, after CI
```

An environment receives every agent, or those matching `spec.agents.selector`. Moving an agent to another host means applying it to another environment. The Agent doesn't change.

`spec.overrides.<agent>` changes an agent in one environment. It can replace model fields and limits, and merge provider fields. It can also take access away, so a stage can run the same agent with less, but never give it more:

- `tools.builtin` keeps only the listed built-in tools, out of the agent's own (`[]` keeps none);
- `tools.mcp` narrows a server's `allowTools`, within the agent's own list when it has one;
- `permissions.ask` adds tools that need approval, which unattended hosts deny.

```yaml
  overrides:
    reviewer:            # a shadow stage that only reads
      tools:
        builtin: [read, grep]
        mcp: [{ref: git-host, allowTools: [get_pull_request]}]
```

An override that would add a tool fails `validate`, naming the field. Overrides change the agent's `resolvedDigest` in that environment, never its `specDigest`.

### Structured answers

An agent that is a step in a pipeline can declare the JSON its final answer must match, with a JSON Schema (draft 2020-12) in `spec.output.schema`: a path to a JSON or YAML file, relative to the agent's file, or the schema itself.

```yaml
spec:
  output:
    schema:
      type: object
      properties:
        verdict: {enum: [approve, request-changes]}
        summary: {type: string}
      required: [verdict, summary]
```

`validate` checks that the schema is valid, self-contained (no `$ref` to another document) and that its root is `type: object`, since the answer is one JSON object. The schema is part of the agent's digests, in whichever form it's written, and environments can't change it. The host enforces it: plan fails on a provider that can't, rather than asking for JSON in the prompt. Today that's [claude-agent-sdk](providers/claude-agent-sdk.md#what-a-run-prints) (and `stub`); see [ADR 0019](adr/0019-agent-output-schema.md).

### Credentials and vaults

An McpServer names the secrets it needs with `secretRef`, in a remote's headers or a package's environment variables. The environment's secret source (`secrets.source`: the caller's environment, or a dotenv file) holds the values. A Vault decides which agents get them:

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Vault
metadata: {name: git-host}
spec:
  credentials:
    - name: git-host-token
      secretRef: GIT_HOST_TOKEN          # read from the secret source
      for: {mcpServer: {ref: git-host}}  # a server that uses GIT_HOST_TOKEN
    - name: gh-cli
      secretRef: GITHUB_PAT
      for: {env: GH_TOKEN}               # the run sees GITHUB_PAT as GH_TOKEN
```

```yaml
kind: Agent
spec:
  tools:
    mcp: [{ref: git-host}]
  vaults: [{ref: git-host}]
```

Once an agent lists a vault, it gets exactly the credentials of its vaults: a secret one of its servers needs that no vault grants for that server fails `validate`. An agent without vaults still gets every secret its servers name, and `validate` warns, so a project can move to vaults one agent at a time. A Vault never holds a value. The digest covers each credential's name, secret name and target, so granting one changes the agent's `resolvedDigest`.

Where a vault's credentials reach the run depends on the provider. Launchers pass only the vault's credentials into the run: see [claude-agent-sdk](providers/claude-agent-sdk.md) and [gitagent](providers/gitagent.md#limits). On [managed-agents](providers/managed-agents.md#vaults), apply keeps a host vault for each Vault, which sessions attach, and `solaria apply --rotate-secrets` sends changed values to it.

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

**Inside the run**, on `claude-agent-sdk` and `gitagent`, the agent and its tools see `SOLARIA_LOGICAL_ID`, `SOLARIA_ENVIRONMENT`, `SOLARIA_SPEC_DIGEST` and `SOLARIA_RESOLVED_DIGEST`, so they can tag what they produce with the version that produced it. Managed Agents environments don't take variables, so runs there don't get them.

**In telemetry**, an environment's `telemetry` sends the identity as OpenTelemetry resource attributes: `gen_ai.agent.name`, `gen_ai.agent.version` and `service.version` (both the `resolvedDigest`), `solaria.agent.spec_digest` and `solaria.environment`. `telemetry.resourceAttributes` adds your own, such as `service.namespace` to keep agents with the same name in different projects apart:

```yaml
  telemetry:
    endpoint: http://collector:4318
    resourceAttributes: {service.namespace: payments}
```

They can't replace Solaria's attributes: `gen_ai.agent.*`, `service.name`, `service.version` and `solaria.*` keys fail `validate`.

The **host binding** maps the logical ID to the host's own identity: an image tag, or a Managed Agents agent ID and version. Providers also write the logical ID and digests onto the host object, in labels or metadata, so a trace or a console view can be traced back to the definition.

## State

`.solaria/state/<environment>.json` records, for each agent: its provider and placement, both digests, the host binding, a `remoteHash` of the host object, the git commit it was applied from, when and by whom, and a history of earlier bindings. Environments whose host keeps a shared object of its own, such as a Managed Agents environment, also have a `host` entry (ADR 0012), and host vaults have `vaults` entries (ADR 0016).

State never holds secret values, only the names of secrets. It's gitignored, and a state lock (`.solaria/state/<environment>.lock`) stops two applies from running at once. Each environment has its own state, so give every developer their own environments, with a name prefix on host objects.

## The workflow

```console
$ solaria validate                # schema, references, files
$ solaria digest -e <environment> # each agent's digests, offline
$ solaria lock -e <environment>   # pin them, for review in git
$ solaria plan -e <environment>   # what apply would do
$ solaria apply -e <environment>  # do it, after confirming
$ solaria status                  # what's deployed where
$ solaria status -e <environment> -a <agent> # how to start it
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

## Starting an agent

Solaria doesn't run agents (ADR 0002). On providers whose agents run where you start them, `claude-agent-sdk` and `gitagent`, apply records the command, and `status -a` shows it with what it needs:

```console
$ solaria status -e local-sdk -a hello
Environment local-sdk (state serial 2)
  OBJECT       PROVIDER                  HOST ID                                    VERSION       DIGEST               APPLIED           CONFIGURATION
  Agent/hello  claude-agent-sdk/process  .solaria/claude-agent-sdk/local-sdk/hello  3f9c1a0d2b7e  sha256:08ffaed7b158  2026-11-01 10:00  up to date

  run: node /work/project/.solaria/claude-agent-sdk/local-sdk/hello/3f9c1a0d2b7e/launch.mjs
  task: after the command with -p, or on stdin
  workspace: the directory it runs in
  environment: ANTHROPIC_API_KEY, GITHUB_TOKEN
  prints: solaria.sh/result/v1
```

Scripts should read `solaria status -o json` rather than the state file, whose format can change. It prints this shape, narrowed by `-e` and `-a`:

```json
{
  "environments": [
    {
      "name": "local-sdk",
      "serial": 2,
      "inConfiguration": true,
      "locked": false,
      "agents": [
        {
          "logicalId": "Agent/hello",
          "provider": "claude-agent-sdk",
          "placement": "process",
          "hostId": ".solaria/claude-agent-sdk/local-sdk/hello",
          "version": "3f9c1a0d2b7e",
          "specDigest": "sha256:08ffaed7b158...",
          "resolvedDigest": "sha256:08ffaed7b158...",
          "appliedAt": "2026-11-01T10:00:00Z",
          "configuration": "upToDate",
          "run": "node /work/project/.solaria/claude-agent-sdk/local-sdk/hello/3f9c1a0d2b7e/launch.mjs",
          "invocation": {
            "argv": ["node", "/work/project/.solaria/claude-agent-sdk/local-sdk/hello/3f9c1a0d2b7e/launch.mjs"],
            "workspace": "cwd",
            "task": {"flag": "-p", "stdin": true},
            "env": ["ANTHROPIC_API_KEY", "GITHUB_TOKEN"],
            "result": "solaria.sh/result/v1"
          }
        }
      ]
    }
  ]
}
```

| Field | |
|---|---|
| `serial` | The state's serial; `0` when the environment has no state yet |
| `inConfiguration` | `false` for an environment that only has state left |
| `locked` | `solaria.lock` pins the environment; each agent's `lockedDigest` is its pinned `resolvedDigest` |
| `host` | The environment's shared host object, when its provider keeps one: `id`, `provider`, `placement`, `hostId`, `version`, `digest`, `appliedAt` |
| `vaults` | On the environment: host vaults, when its provider keeps them, with the same fields as `host` and `usedBy`, the agents whose sessions attach each. On an agent: the vaults to attach when starting a session, as `name` and `hostId`. |
| `annotations` | The agent's `metadata.annotations`, read from the configuration rather than state, since they're never applied. Absent for an agent the configuration no longer has. |
| `configuration` | `upToDate`, `changed`, `overridesChanged` (only the environment's overrides), `removed` (from the configuration), `notApplied`, or `noLongerSelected` (pinned in `solaria.lock`, neither applied nor selected). An agent that isn't applied has only `logicalId`, `annotations`, `configuration` and `lockedDigest`. |
| `run` | The command for a POSIX shell, run from the workspace. The task goes after it. |
| `invocation` | The same command, for programs. Absent for providers whose host runs the agent, and for agents applied before Solaria recorded it. |

The `invocation`:

| Field | |
|---|---|
| `argv` | The command and its arguments, without the task. `{workspace}` in an argument stands for the workspace's absolute path. |
| `workspace` | `cwd`: run the command in the workspace. `mount`: `argv` mounts it through `{workspace}`. |
| `task` | `flag` comes before the task, as in `-p "Review the diff."`. With `stdin`, the command also reads the task from stdin when the flag isn't given. |
| `env` | Variables the command reads from the caller's environment |
| `envFile` | The dotenv file the secrets come from |
| `result` | What the command prints on stdout, when it's a format Solaria defines: `solaria.sh/result/v1` is the launcher's JSON (see [claude-agent-sdk](providers/claude-agent-sdk.md#what-a-run-prints)) |

Both name secrets, never their values.

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
