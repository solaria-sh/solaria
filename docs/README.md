# Solaria documentation

- [Concepts](concepts.md): resources, providers and placements, the sandbox, identity, state, the workflow, starting an agent, and pinning environments with `solaria.lock`.
- [Getting started](getting-started.md): install Solaria and take one agent through validate, plan, apply and destroy, with no credentials.
- [Editors and coding agents](editors.md): completion and field checks from the published schema, and `solaria validate` from an editor or a coding agent.
- Resources, every field of each kind, generated from the schema:
  - [`Agent`](resources/agent.md): what the agent is, independently of any host;
  - [`Environment`](resources/environment.md): where agents run, and everything about that place;
  - [`McpServer`](resources/mcp-server.md): an MCP server that agents refer to by name;
  - [`Vault`](resources/vault.md): credentials that agents use by reference.
- Providers:
  - [`stub`](providers/stub.md): files only, for trying Solaria and for tests;
  - [`gitagent`](providers/gitagent.md): a container image that runs gitagent;
  - [`claude-agent-sdk`](providers/claude-agent-sdk.md): the Claude Agent SDK, as a local Node or Python process or in a container image;
  - [`managed-agents`](providers/managed-agents.md): Claude Managed Agents, in the cloud or with a self-hosted sandbox.
- [Architecture decision records](adr/): why Solaria works the way it does.
- [Spikes](spikes/): what was learned about each host before building its provider.

Examples:

- [`examples/hello`](../examples/hello): the smallest project, runnable with no credentials.
- [`examples/reviewer`](../examples/reviewer): one agent deployed unchanged to gitagent, Managed Agents cloud and Managed Agents self-hosted.
