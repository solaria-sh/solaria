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

**Digest input format `solaria.sh/definition/v1`** (implemented in `internal/resolve`). The format name is part of the input, so a new format gives new digests on purpose. Normalization rules:

- **Left out:** the agent's name and labels (the logical ID covers who it is), the environment's sandbox, secrets and telemetry, and secret values.
- **Instructions:** file contents in the listed order. File names don't count, so renaming a file keeps the digest.
- **Skills:** keyed by skill name. Each holds a map of file path (forward slashes) to SHA-256. Hidden files and folders are skipped, and so are file modes.
- **Line endings:** CRLF becomes LF in instruction and skill files that contain no NUL byte, so Windows and Unix checkouts agree.
- **Order:**
  - sorted where order carries no meaning: `tools.builtin`, `allowTools`, `permissions.ask`;
  - kept where it does: instructions, `model.fallback`.
- **MCP servers:** the referenced McpServer spec, keyed by name.
- **Values:** `limits.timeout` is normalized (`90m` and `1h30m` are equal). Empty lists encode as `[]`.
- **Overrides:**
  - `model` and `limits` replace field by field;
  - `providers` merges as an [RFC 7386](https://www.rfc-editor.org/rfc/rfc7386) JSON Merge Patch, where `null` removes a field.

### Observability

This follows the [OpenTelemetry GenAI conventions](https://github.com/open-telemetry/semantic-conventions-genai) (currently at Development stability):

- `gen_ai.agent.id` = the host's resource ID (OTel defines it as the hosted agent resource's identifier);
- `gen_ai.agent.name` = the logical name;
- `gen_ai.agent.version` = `resolvedDigest`;
- namespaced extras: `solaria.agent.spec_digest`, `solaria.environment`.

Providers inject these where the runtime allows it. Neither v0 runtime fully allows it today (M1 spikes):

- **gitagent** ignores `OTEL_RESOURCE_ATTRIBUTES` and sends only `service.name` and `service.version`. The provider sets `OTEL_SERVICE_NAME=<agent name>`, and we'll propose upstream support for the standard variable.
- **Managed Agents** spans already carry `gen_ai.agent.id` and `gen_ai.agent.name`, but no agent version, and destinations can't add attributes.

So **Solaria's state is the join key**: a trace's host ID (plus the session's pinned version, where known) maps through the binding `history` to `resolvedDigest`. An OTel Collector `resource` processor can attach the attributes downstream when needed.

### Host metadata

Providers write the logical ID and both digests onto host objects where possible (for example, Managed Agents' agent `metadata`). That enables drift detection, import later, and finding orphaned objects.

Signing digests (sigstore/cosign, A2A signed Agent Cards) is deferred.

### Consequences

- Good, because the same inputs give the same digests on any machine.
- Good, because traces can be grouped by logical agent across hosts and still tell versions apart.
- Bad, because canonicalization must be exact. A change in how inputs are assembled changes every digest, so the digest input format is versioned.

### Confirmation

Golden tests, in `internal/resolve`, check that:

- reordering keys, documents, files or unordered lists leaves the digest unchanged;
- changing line endings or adding a hidden file leaves it unchanged;
- changing one byte of a skill changes it;
- environment overrides change `resolvedDigest` but not `specDigest`.

The golden files pin the digests themselves, so an accidental format change fails the build.

**What the digests don't cover.** The environment's sandbox, secrets and telemetry are left out, so the same agent has the same digest everywhere. A host still needs to notice when they change. Providers do that with digests of their own, kept in the binding's outputs. For example, gitagent keeps one each for the sandbox, the generated image and the run command. A provider plans an update when one of them moves, even though the agent's digests stay the same.

## More information

- [RFC 8785: JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785)
- [OTel GenAI attributes](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/registry/attributes/gen-ai.md)
- Revised in M5: how providers track environment changes the digests leave out.
