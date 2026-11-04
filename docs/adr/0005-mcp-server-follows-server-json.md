---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# McpServer follows the MCP Registry `server.json` shape

## Context and problem statement

MCP defines two standard transports, stdio (the client starts the server as a subprocess) and Streamable HTTP. Hosts differ in what they support. Managed Agents' `mcp_servers` accepts only `type: url` remote endpoints, while gitagent starts stdio servers itself. An McpServer resource that models only one transport would make agents unportable.

## Decision drivers

- Support both standard transports.
- Reuse an existing schema instead of inventing one.
- Fail early, at plan time, when a host can't reach a server.

## Considered options

- One transport per McpServer resource.
- A custom union type.
- The MCP Registry `server.json` shape: `packages` and `remotes`.

## Decision outcome

Chosen option: "The `server.json` shape", because the official MCP Registry already describes servers this way, and a single server may offer both forms:

- **`packages`:** something to install and run locally (`registryType`, `identifier`, `version`, `transport: {type: stdio}`, arguments and environment variables). v0 supports npm and PyPI packages.
- **`remotes`:** an endpoint to connect to (`type: streamable-http`, `url`, `headers`). Header values that are secrets use `secretRef`.

The provider picks a transport it supports for its placement. If a server offers none, **`plan` fails** and names the server and the missing transport. stdio servers run inside the sandbox, so `validate` checks that the environment provides their runtime (Node for npm, Python for PyPI).

Agents narrow a server per agent with `allowTools`. Managed Agents maps this to `mcp_toolset.configs[].enabled`.

Deferred: OCI stdio packages (they need Docker inside the sandbox), and a stdio→HTTP bridge that would expose a local package to remote-only hosts. On cloud placements that bridge needs a publicly reachable endpoint, which is an infrastructure decision, not something to hide inside a provider. A later version may also accept references to entries in the official registry.

### Consequences

- Good, because one definition works on stdio-only and remote-only hosts when the server offers both.
- Good, because it's compatible with the registry format if we import from it later.
- Bad, because the `server.json` schema is still evolving (schema date 2025-12-11), so the subset we accept must be versioned with our API.

### Confirmation

Tests cover transport selection for every provider and placement, including the plan-time failure.

## More information

- [MCP transports, 2025-11-25 specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
- [`server.json` format](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/generic-server-json.md)
- [Managed Agents MCP connector](https://platform.claude.com/docs/en/managed-agents/mcp-connector)
