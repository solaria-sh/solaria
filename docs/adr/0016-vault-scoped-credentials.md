---
status: proposed
date: 2026-11-10
decision-makers: Renan Tarouco da Fonseca
---

# A Vault resource scopes credentials to the agents that reference it

## Context and problem statement

Agents need credentials: a token for a remote MCP server, a variable for a CLI the agent runs. Today an McpServer header names a `secretRef`, and the environment's secret source (`env` or a dotenv file) supplies the value. That works where Solaria's launcher starts the run, and the launcher already passes each agent only the secrets its servers name. Managed Agents is different: agents there hold no credentials. Whoever starts a session attaches vaults, and the host substitutes credentials when the agent calls out. Solaria only warns ("give sessions a vault"), so remote MCP servers that need a credential can't be used there through Solaria. ADR 0004 planned a host-backed `Vault` kind for this. How does a project declare credentials, scope them to agents, and deliver them to each host, while secret values still never reach state, digests, logs or plans?

## Decision drivers

- Least privilege: an agent gets only the credentials it needs, not every secret its environment has.
- One declaration that works on every host: where Solaria's launcher runs, and where a host's vault does the work.
- Values never reach state, digests, logs, plan output or rendered files. Only names do.
- Granting a credential is reviewed in git, like any other change to what an agent can do.

## Considered options

- Derived vaults: no new kind; on hosts that need vaults, apply creates one per agent holding exactly the secrets its MCP servers name.
- A `Vault` kind that agents reference.
- A `Vault` kind bound per environment, shared by every agent in it.

## Decision outcome

Chosen option: "A `Vault` kind that agents reference", because granting a credential becomes an explicit, reviewable reference, and a vault can be shared on purpose by the agents that need the same access.

**The resource.** A config kind that lists credentials. Each credential says what it unlocks, which matches the two kinds of credential Managed Agents' vaults hold (an MCP server's, matched by its URL, and an environment variable), and lets a launcher scope it exactly:

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Vault
metadata: {name: tracker}
spec:
  credentials:
    - name: tracker-api
      secretRef: TRACKER_TOKEN
      for: {mcpServer: {ref: tracker}}
    - name: git-host
      secretRef: GIT_TOKEN
      for: {env: GIT_TOKEN}
```

- `secretRef` names a value in the environment's secret source, as McpServer headers do today. Values always come from there; a Vault never holds one.
- `for` takes exactly one target. An `mcpServer` target must be a server that has a remote, and that declares a `secretRef` header for this credential to fill.

**References.** An agent lists its vaults in `spec.vaults: [{ref: tracker}]`, the same `ref:` shape as everything else (ADR 0004). An unresolved reference fails `validate`. The definition digest covers each referenced vault's name and its credentials' names and targets, never values, so granting or removing a credential changes `resolvedDigest` and shows in `solaria.lock` (ADR 0013).

**Scoping.** An agent gets the credentials of the vaults it references and nothing else.
- On `process` and `docker`, the launcher passes only those credentials into the run. A secret an MCP server header names but no referenced vault grants fails `validate`, naming the server and the header. The environment's secret source still supplies the values.
- On Managed Agents, apply creates one host vault per Vault and environment, shared only by the agents that reference it. It's a host-backed resource (ADR 0004) with its own state entry: host ID, version and the credential names it holds. The invocation (ADR 0014) lists the vault IDs a caller attaches when starting a session. An MCP server whose credential no referenced vault provides fails plan, where it warns today.

**Values and rotation.** State and digests can't tell whether a value changed: they never see it, not even as a hash. Apply sends values to a host vault when the vault or a credential is created, or when a credential's name or target changes. `solaria apply --rotate-secrets` sends every value again. Plans show a vault as changing only in those cases, so rotation doesn't make every plan noisy.

**Secrets that aren't credentials** keep working as today: an McpServer `env` entry or a header without a Vault on `process` and `docker` is still read from the secret source, but `validate` reports it so it can move to a Vault. A later version may make Vault references mandatory.

### Consequences

- Good, because remote MCP servers that need a credential work on Managed Agents, and plan fails early when one is missing.
- Good, because "which agents can use this token" is answered by searching for one name, and changes to it are reviewed diffs.
- Good, because the same Vault drives the launcher on local placements and the host vault on Managed Agents.
- Bad, because a rotated value doesn't reach a host vault until someone runs `apply --rotate-secrets`. Solaria can't detect it.
- Bad, because it adds a kind, a host-backed state entry, and a reference every agent with credentials must declare.

### Confirmation

- Validation tests cover an unresolved vault, a credential whose target doesn't match, and a header secret no vault grants.
- Golden tests for each provider show the scoped credentials: the launcher's allowlist on `process` and `docker`, and the vault requests on Managed Agents.
- A test checks that no secret value appears in state, plan output or rendered files.
- The managed-agents fake API test covers creating a vault, a rename and `--rotate-secrets`.

## Pros and cons of the options

### Derived vaults

- Good, because there's nothing new to write: scoping follows from the MCP servers an agent uses.
- Bad, because a credential that isn't for an MCP server, such as a variable a CLI needs, has nowhere to go.
- Bad, because granting access is implicit, a side effect of adding a server.

### A `Vault` kind bound per environment

- Good, because one vault per environment is simplest to set up.
- Bad, because every agent in the environment sees every credential, which defeats per-agent scoping.

## More information

- [ADR 0004](0004-resource-model.md): host-backed kinds and references.
- [ADR 0005](0005-mcp-server-follows-server-json.md): MCP credentials per host.
- [ADR 0009](0009-state.md): no secrets in state.
- [ADR 0014](0014-process-placement-and-claude-agent-sdk.md): the launcher's allowlist and the invocation.
- [Managed Agents overview](https://platform.claude.com/docs/en/managed-agents/overview), for vaults attached when a session starts.
