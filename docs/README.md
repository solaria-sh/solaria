# Solaria documentation

- [Concepts](concepts.md): resources, providers and placements, the sandbox, identity, state and the workflow.
- [Card reference](reference.md): every field of every kind, generated from the schema.
- Providers:
  - [`stub`](providers/stub.md): files only, for trying Solaria and for tests;
  - [`gitagent`](providers/gitagent.md): a container image that runs gitagent;
  - [`managed-agents`](providers/managed-agents.md): Claude Managed Agents, in the cloud or with a self-hosted sandbox.
- [Architecture decision records](adr/): why Solaria works the way it does.
- [Spikes](spikes/): what was learned about each host before building its provider.

Examples:

- [`examples/hello`](../examples/hello): the smallest project, runnable with no credentials.
- [`examples/reviewer`](../examples/reviewer): one agent deployed unchanged to gitagent, Managed Agents cloud and Managed Agents self-hosted.
