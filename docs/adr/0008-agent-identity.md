---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# Agent identity: logical ID, two digests, host binding

## Context and problem statement

The same agent may run on a laptop, in staging on one host and in production on another. Traces, evals and rollbacks need to identify the agent, and the exact definition, independently of where it runs. Each host has its own IDs and versions, for example Managed Agents' agent ID and version number.

## Decision drivers

- Identity that's stable across hosts.
- The ability to tell exactly which definition is running.
- Reuse of observability conventions.
- No secrets in identifiers.

## Considered options

- Host IDs and versions only.
- A semantic version written by hand.
- A logical ID plus content digests, with a host binding recorded in state.

## Decision outcome

Chosen option: "A logical ID plus content digests". An agent's identity has three layers:

| Layer | Answers | Value |
|---|---|---|
| Logical ID | Which agent? | `Agent/<metadata.name>`; never changes |
| Content digests | Exactly which definition? | `sha256:` over the RFC 8785 canonical JSON of the agent |
| Host binding | Where, and as what? | IDs and versions returned by the provider, stored in state (ADR 0009) |

There are two digests:

- **`specDigest`:** the agent and everything it references, **before** environment overrides. It means "what we wrote", and stays the same in every environment.
- **`resolvedDigest`:** the same, **after** overrides. It means "what actually runs here."

What the digest input includes and excludes:

- **Included:** the contents of instruction files, a map of skill file paths to SHA-256, the resolved McpServer specs, the model binding, permissions and limits.
- **Secrets:** only their reference names, never their values.
- **Excluded:** the provider and placement, so the digest identifies the agent independently of the host.

### Observability

This follows the [OpenTelemetry GenAI conventions](https://github.com/open-telemetry/semantic-conventions-genai) (currently at Development stability):

- `gen_ai.agent.id` = the host's resource ID (OTel defines it as the hosted agent resource's identifier);
- `gen_ai.agent.name` = the logical name;
- `gen_ai.agent.version` = `resolvedDigest`;
- namespaced extras: `solaria.agent.spec_digest`, `solaria.environment`.

Providers inject these where the runtime allows it, for example through `OTEL_RESOURCE_ATTRIBUTES`.

### Host metadata

Providers write the logical ID and both digests onto host objects where possible (for example, Managed Agents' agent `metadata`). That enables drift detection, import later, and finding orphaned objects.

Signing digests (sigstore/cosign, A2A signed Agent Cards) is deferred.

### Consequences

- Good, because the same inputs give the same digests on any machine.
- Good, because traces can be grouped by logical agent across hosts and still tell versions apart.
- Bad, because canonicalization must be exact. A change in how inputs are assembled changes every digest, so the digest input format is versioned.

### Confirmation

Golden tests: reordering keys or files leaves the digest unchanged; changing one byte of a skill changes it; environment overrides change `resolvedDigest` but not `specDigest`.

## More information

- [RFC 8785: JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785)
- [OTel GenAI attributes](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/registry/attributes/gen-ai.md)
