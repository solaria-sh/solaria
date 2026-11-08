# Solaria documentation

- [Concepts](concepts.md): resources, providers and placements, the sandbox, identity, state and the workflow.
- [Getting started](getting-started.md): install Solaria and take one agent through validate, plan, apply and destroy, with no credentials.
- Resources, every field of each kind, generated from the schema:
  - [`Agent`](resources/agent.md): what the agent is, independently of any host;
  - [`Environment`](resources/environment.md): where agents run, and everything about that place;
  - [`McpServer`](resources/mcp-server.md): an MCP server that agents refer to by name.
- Providers:
  - [`stub`](providers/stub.md): files only, for trying Solaria and for tests;
  - [`gitagent`](providers/gitagent.md): a container image that runs gitagent;
  - [`managed-agents`](providers/managed-agents.md): Claude Managed Agents, in the cloud or with a self-hosted sandbox.
- [Architecture decision records](adr/): why Solaria works the way it does.
- [Spikes](spikes/): what was learned about each host before building its provider.

Examples:

- [`examples/hello`](../examples/hello): the smallest project, runnable with no credentials.
- [`examples/reviewer`](../examples/reviewer): one agent deployed unchanged to gitagent, Managed Agents cloud and Managed Agents self-hosted.
