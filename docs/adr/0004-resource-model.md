---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# Resource model: envelope, kinds, references, provider passthrough

## Context and problem statement

Agents, the places they run, and the tools they share all need a declarative, versionable format that people and coding agents can both write, editors can validate, and every provider can read.

## Decision drivers

- Easy for people and LLMs to write.
- A versioned schema with editor validation.
- Reusable pieces (one MCP server shared by many agents) without copy-paste.
- Portability by default, but no lowest-common-denominator agents.
- Reuse existing standards for instructions, skills and tools.

## Considered options

- Format: HCL, plain YAML, or YAML with a Kubernetes-style envelope.
- Reuse: inline definitions, references, or both.

## Decision outcome

### Envelope

Every resource is a YAML document shaped like this:

```yaml
apiVersion: solaria.sh/v1alpha1   # domain is provisional until registered
kind: Agent
metadata: { name: triage, labels: { … } }
spec: { … }
```

This is the established shape for declarative resources (Kubernetes, Crossplane, kagent). The API group is versioned and `kind` tells types apart. The Go types in `internal/api/v1alpha1` generate the JSON Schema. HCL reads well, but YAML is easier for agents and tools to write.

Multiple documents may live in one file. Solaria discovers YAML files whose `apiVersion` group is `solaria.sh` and ignores the rest.

### Kinds in v0

- **Agent:** what the agent is:
  - `description`;
  - `instructions`: Markdown files, concatenated in order, AGENTS.md style;
  - `model`: `preferred`, `fallback`, `effort`, using `provider:model` strings;
  - `tools`: abstract `builtin` names, plus `mcp` references;
  - `skills`: paths to [Agent Skills](https://agentskills.io) folders;
  - `permissions` and `limits`.
- **Environment:** where and how agents run: `provider`, `placement`, `sandbox` (ADR 0007), `secrets`, `telemetry`, `overrides.<agent>` and an optional label `selector`.
- **McpServer:** a reusable MCP server definition (ADR 0005).

The Agent and Environment split follows the hosts themselves. Managed Agents, for example, keeps model, system prompt, tools, skills and MCP on the agent, while budgets and vaults are set when a session starts.

### References

Agents refer to other resources by name, through `ref:` only:

```yaml
tools:
  mcp:
    - ref: ag-llama
      allowTools: [get_ticket, add_comment]   # narrowing per agent
```

Once a kind exists, inline definitions of it aren't allowed, so there's one way to do it. An unresolved reference fails `validate`.

Reusable kinds come in two sorts:

- **Config-only** (McpServer, and later ModelProfile and PermissionPolicy): resolved during normalization, with no state entry.
- **Host-backed** (Skill, which v0.2 adds together with a dependency graph, and later Vault): get their own state entry and creation order.

### Provider passthrough

`spec.providers.<name>` holds raw fields that are passed unchanged to one provider, for example Managed Agents' `inference_geo`. Without it, cards could only use features every host supports. `validate` reports cards that use passthrough, so lost portability is never silent.

An Environment has its own `spec.providers.<name>` for **host settings**, such as the platform gitagent builds for. They describe where agents run, not the agents, so they're not part of any digest, and `validate` doesn't report them: an environment is host-specific by nature. Each provider checks its own settings at plan time.

### Consequences

- Good, because instructions, skills and tools reuse AGENTS.md, Agent Skills and MCP instead of inventing formats.
- Good, because the `ref:` shape stays the same as new kinds arrive, so cards don't break.
- Bad, because YAML is verbose and error-prone. That's mitigated by strict decoding and line-numbered errors.

### Confirmation

`schemas/v1alpha1/` is generated from the Go types and CI fails when it drifts. Validation tests cover unknown fields, unresolved references and duplicate names.

## More information

- [AGENTS.md](https://agents.md/), [Agent Skills](https://agentskills.io), [MCP](https://modelcontextprotocol.io)
- [Managed Agents: agent setup](https://platform.claude.com/docs/en/managed-agents/agent-setup)
- Revised in M5: Environment `providers` for host settings.
