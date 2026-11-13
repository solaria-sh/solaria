---
status: accepted
date: 2026-11-11
decision-makers: Renan Tarouco da Fonseca
---

# A Vault resource scopes credentials to the agents that reference it

> [ADR 0020](0020-targets-and-environments.md) renamed the `Environment` kind this ADR calls an environment to `Target`, and `-e/--environment` to `-t/--target`.

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
- `for` takes exactly one target. An `mcpServer` target must be a server that uses this `secretRef`, in a remote's header or a package's environment variable; the server reads it under the secret's own name. An `env` target sets the named variable in the run from the secret, which may be named differently. A `gitHost` target (ADR 0017) authenticates clones of repositories on that host, and never reaches the run; Managed Agents vaults don't hold it.

**References.** An agent lists its vaults in `spec.vaults: [{ref: tracker}]`, the same `ref:` shape as everything else (ADR 0004). An unresolved reference fails `validate`. The definition digest covers each referenced vault's name and its credentials' names, secret names and targets, never values, so granting or removing a credential changes `resolvedDigest` and shows in `solaria.lock` (ADR 0013). An agent without vaults has no `vaults` field in its definition, so adding the kind changes no existing digest (ADR 0008).

**Scoping.** An agent that references any vault gets the credentials of those vaults and nothing else. A secret one of its MCP servers uses, in a header or a package variable, that no referenced vault grants for that server fails `validate`, naming the server and the secret.
- On `process` and `docker`, the launcher passes only those credentials into the run, and the environment's secret source still supplies the values. claude-agent-sdk's prelude drops everything else from the run's environment, whatever the source. gitagent can't rename a variable, so on gitagent an `env` target must be named after its secret, or plan fails; and `docker --env-file` hands the container the whole dotenv file, so plan warns that only `secrets.source: env` scopes credentials there.
- On Managed Agents, apply creates one host vault per Vault and environment, shared only by the agents that reference it. It's a host-backed resource (ADR 0004) with its own state entry, `vaults` in the environment's state: host ID, credential IDs, and a config of credential names, secret names and targets. Providers whose host keeps vaults implement `provider.VaultHost`, and the engine plans vaults like the environment's own host object (ADR 0012): created and updated before the agents, deleted after them. Managed Agents records no invocation, since the host runs the agent, so `status` lists the vault IDs a caller attaches when starting a session, per agent.
- A Managed Agents vault holds two kinds of credential. An `mcpServer` credential becomes a static bearer token for the server's remote URL, which the host sends as `Authorization: Bearer <token>`. So the server must take the secret in its `Authorization` header, or plan fails, and a leading `Bearer ` in the value is dropped, so the same value works where a launcher sends the header as it is. A secret only an MCP package uses isn't stored, with a warning: the host runs no packages. An `env` credential becomes an environment variable credential whose value the host substitutes into requests to any host the environment's network policy allows, so `sandbox.network` bounds it as it bounds everything else.
- An agent on Managed Agents whose MCP servers send a secret header and that lists no vaults gets a warning, as on every host; one that lists vaults is checked by `validate` like everywhere else.

**Values and rotation.** State and digests can't tell whether a value changed: they never see it, not even as a hash. Apply sends values to a host vault when the vault or a credential is created, or when a credential's secret or target changes; a target the host can't change, such as a variable's name, recreates the credential. `solaria apply --rotate-secrets` sends every value again, and `plan --rotate-secrets` shows it. Apply reads every value it will send before its first host call, so a missing secret changes nothing. Plans show a vault as changing only in those cases, so rotation doesn't make every plan noisy.

**Agents without vaults** keep working as today: the launcher reads every secret their MCP servers use from the secret source, and `validate` warns for each server so its secrets can move to a Vault. Strict scoping starts with an agent's first vault reference, so a project can adopt vaults one agent at a time. A later version may make Vault references mandatory.

### Consequences

- Good, because remote MCP servers that need a credential work on Managed Agents, and plan fails early when one is missing.
- Good, because "which agents can use this token" is answered by searching for one name, and changes to it are reviewed diffs.
- Good, because the same Vault drives the launcher on local placements and the host vault on Managed Agents.
- Bad, because a rotated value doesn't reach a host vault until someone runs `apply --rotate-secrets`. Solaria can't detect it.
- Bad, because it adds a kind, a host-backed state entry, and a reference every agent with credentials must declare.

### Confirmation

- Validation tests cover an unresolved vault, a credential whose target doesn't match, and, for an agent with vaults, a server secret no vault grants.
- Golden tests for each provider show the scoped credentials: the launcher's allowlist on `process` and `docker`, and the vault requests on Managed Agents.
- A test checks that no secret value appears in state, plan output or rendered files.
- The managed-agents fake API test covers creating a vault, an unchanged plan, `--rotate-secrets`, a changed secret and variable, a missing secret, a vault deleted outside Solaria, and destroy, and checks that no value reaches the transcript or the state.

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
- [ADR 0017](0017-run-inputs.md): the `gitHost` target, for repository clones.
