---
status: proposed
date: 2026-11-10
decision-makers: Renan Tarouco da Fonseca
---

# An agent can declare the JSON its answer must match

## Context and problem statement

Some agents are steps in a pipeline: whatever runs them parses the answer, so the answer must be exactly one JSON object of a known shape, such as a triage verdict. Today the only way to ask for that is in the instructions, which no host enforces. Several hosts can enforce it: the Claude Agent SDK takes a JSON Schema and returns the answer as structured output. How does an Agent declare its output contract, how is it part of the agent's identity, and what happens on a host that can't enforce it?

## Decision drivers

- The contract is part of what the agent is, so it belongs on the Agent and in its digests (ADR 0008), not on the Environment.
- A consumer that relies on the contract must never get an unchecked answer silently.
- Schemas can be long and shared between agents, or short and specific to one.
- Existing digests and lock files (ADR 0013) shouldn't move for agents that don't use it.

## Considered options

- `spec.output.schema` as a path to a schema file.
- `spec.output.schema` as an inline JSON Schema.
- Either form, normalized to the same digest input.
- Instructions only (no field).

## Decision outcome

Chosen option: "Either form, normalized to the same digest input", because short schemas read best inline and long or shared ones belong in a file, and normalizing both to the parsed schema means the choice never changes the agent's identity.

- **Field:** `spec.output.schema` on the Agent. A string is a path to a JSON or YAML file, relative to the file that declares the agent, like instructions. An object is the JSON Schema itself, written in YAML.
- **Validation:** `solaria validate` checks that the file exists and parses, and that the result is a valid JSON Schema (draft 2020-12) whose root is an object schema (`type: object`), since the answer is one JSON object.
- **Identity:** the schema joins the digest input as an optional `output` member holding the parsed schema, canonicalized like the rest of the definition (RFC 8785). The file's name, its format (JSON or YAML) and whether it was inline don't count. The member is left out when the agent has no schema, so existing definitions encode to the same bytes, every existing digest stays the same, and `solaria.lock` files stay valid. The golden tests in `internal/resolve` must confirm that. If they don't, the definition format moves to `v2`, as ADR 0008 requires.
- **Environments can't change it:** overrides only narrow an agent's access ([ADR 0004](0004-resource-model.md#overrides-only-narrow-access)), and the output contract isn't access. An environment that can't honour it is the wrong environment for the agent.
- **Capability:** a provider declares whether its host enforces an output schema. When an agent has a schema and the environment's provider doesn't, `plan` fails for that agent before any host call, naming the provider and the field. It never falls back to asking for JSON in the prompt.
- **`claude-agent-sdk`:** the launcher passes the schema as the SDK's structured output format (`outputFormat` in TypeScript, `output_format` in Python), and the answer is the result message's structured output. The provider fails `plan` on schema keywords its host is known not to support, rather than at run time.
- **Other providers:** `managed-agents` and `gitagent` start without the capability. Each gains it when its host is checked to enforce a schema, with golden tests of what it sends.

### Consequences

- Good, because the output contract is versioned with the agent: changing it is a new `resolvedDigest`, visible in `plan` and in the lock file.
- Good, because a pipeline never runs an agent whose contract the host would ignore.
- Bad, because an agent with a schema can't run on hosts without the capability until their provider supports it.
- Bad, because hosts enforce different subsets of JSON Schema, so a schema valid for `validate` can still fail `plan` for one provider.

### Confirmation

- Golden tests in `internal/resolve`: an inline schema and the same schema in a JSON or a YAML file give the same digests; an agent without a schema keeps its current digests.
- A capability test fails `plan` for an agent with a schema on a provider without the capability.
- `claude-agent-sdk` golden files show the schema in the rendered options, for both launchers.

## Pros and cons of the options

### A file path only

- Good, because schemas are reusable and diffable on their own.
- Bad, because a three-field schema needs a file of its own.

### Inline only

- Good, because the agent card is self-contained.
- Bad, because long schemas bury the rest of the card, and sharing one means copying it.

### Instructions only

- Good, because there's nothing to build.
- Bad, because nothing enforces it, and a malformed answer reaches the consumer.

## More information

- [ADR 0004](0004-resource-model.md): the resource model, and overrides that only narrow access.
- [ADR 0008](0008-agent-identity.md): the digests and their input format.
- [ADR 0013](0013-lock-file-pins-digests.md): the lock file.
- Claude Agent SDK: [structured outputs](https://code.claude.com/docs/en/agent-sdk/structured-outputs).
