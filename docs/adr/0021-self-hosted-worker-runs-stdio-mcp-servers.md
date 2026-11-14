---
status: proposed
date: 2026-11-14
decision-makers: Renan Tarouco da Fonseca
---

# The Managed Agents self-hosted worker runs stdio MCP servers and serves their tools

## Context and problem statement

Many MCP servers ship only as stdio packages, on npm or PyPI. Managed Agents can't start them: its `mcp_servers` takes only URLs, and Anthropic opens the MCP connections, not the worker (ADR 0005). So an agent whose server has no `remotes` fails plan on both placements. ADR 0005 deferred a stdio-to-HTTP bridge, because on a remote-only host it needs an endpoint Anthropic can reach, which is an infrastructure decision.

Managed Agents now documents a second route for self-hosted sandboxes: the worker becomes the MCP client and serves the server's tools as custom tools. The agent declares those tools, emits `agent.custom_tool_use` when it calls one, and the worker forwards the call to the server and posts the result. Anthropic never connects to the server. How should Solaria run an agent's stdio servers on Managed Agents?

## Decision drivers

- Agents keep one definition: a server written once runs wherever a provider can start it (ADR 0005).
- Nothing Solaria builds opens an inbound endpoint or depends on a research preview.
- Plans stay honest. A host enforces what the agent asks for or refuses it, and never drops it.
- Credentials stay scoped to the agents that are granted them (ADR 0016).
- The adopter's agents run on `managed-agents/self-hosted` and use stdio-only servers.

## Considered options

- The self-hosted worker runs the stdio servers and serves their tools as custom tools.
- A stdio-to-HTTP bridge container that Solaria runs, reached through an MCP tunnel.
- A stdio-to-HTTP bridge with a public endpoint.
- Keep failing plan, and require servers with `remotes`.

## Decision outcome

Chosen option: "The self-hosted worker runs the stdio servers", because the worker already runs inside the user's boundary and has a documented, supported way to serve tools. It needs no endpoint, no tunnel and no new authentication. It covers `self-hosted` only. `cloud` has no worker, so a stdio-only server keeps failing plan there.

### Selection

The provider picks a transport it supports, as ADR 0005 says. On `self-hosted` it uses the server's remote when it has one, and runs the server on the worker only when it has just stdio packages. Plan notes which servers run on the worker. `cloud` is unchanged.

### The worker image

The worker image of each Environment (ADR 0020) installs the stdio servers of every agent bound to it, at the versions their `packages` pin. npm packages are installed with npm, PyPI packages into their own virtual environment. The Environment's digest covers the servers' packages, arguments and the names of their variables, so a change rebuilds the image and restarts the worker. Nothing is downloaded when a session starts, and the image needs network to the registries only at build time.

### Tools on the agent

Each server's tools are declared on the agent as custom tools named `<server>__<tool>`, the form gitagent uses, so `allowTools` and traces read the same on every host. `allowTools` narrows what's declared, as it narrows `mcp_toolset.configs` for a remote server.

Custom tool declarations must fit the Managed Agents API, so plan or apply fails, naming the server and the tool, when:

- a name is longer than 128 characters;
- an input schema uses `$ref`, or a top-level `oneOf`, `anyOf` or `allOf`;
- the agent would have more than 256 tools, counting the built-in toolset as one.

### Listing the tools

Solaria needs each server's tools before it can declare them. Plan needs no Docker today, and nothing but the host's API to refresh, so it shouldn't build images or run third-party code. So **apply lists them**: after building the worker image, it runs each server in that image once, lists its tools, and declares them on the agent. A server that fails to start or list fails apply before the agent changes.

State records the list on the Environment's host entry, keyed by server, with the package identity it was listed from. Later plans render the agent from the recorded list, so its tool changes show as field diffs and drift is caught as for any agent field. When a server's package, version or arguments change, plan says its tools are listed again at apply, and the agent's step shows the change it knows about.

### Running a call

The worker serves the wrapped tools to every session it claims, through the SDK's per-session `ToolsFunc`. It starts a session's stdio server when that session first calls one of the server's tools, in the session's workdir, and stops it when the session ends. Sessions don't share a server process. A call the server fails becomes an error tool result, and each call has a timeout, so a hung server doesn't hang the session.

### Secrets

A server's `environmentVariables` that use `secretRef` reach the worker container from the target's secret source, through docker's environment as the environment key does, never its command line, state or the image. The worker passes each server only its own variables. With vaults (ADR 0016), a credential `for: {mcpServer: …}` grants the secret to a server that runs on the worker, instead of being dropped with a warning as it is today.

One worker serves every agent in its Environment, so a server's process gets the same secrets whichever agent calls it. Plan fails when two agents in one Environment use the same worker-run server but are granted different credentials for it; the error suggests separate Environments.

### Approvals

Permission policies don't apply to custom tools: the worker runs every call. So plan fails when an agent's `permissions.ask` names a tool of a server that runs on the worker, and the error suggests a server with `remotes`.

### Consequences

- Good, because agents can use stdio-only servers on Managed Agents without any endpoint, tunnel or extra service.
- Good, because the tools run in the user's network, next to the agent's bash and files, which is what self-hosting is for.
- Good, because a pinned package gives the same tools on every apply, and the image digest records exactly what runs.
- Good, because no digest changes: the McpServer is already part of the definition.
- Bad, because `cloud` still can't use stdio-only servers.
- Bad, because apply runs third-party code to list tools, and plan can't show a new server's tools until apply has run once.
- Bad, because the server's tool names, descriptions and results enter the model's context and can steer `bash` on the worker host. Only declared tools are served, and the docs say to run only servers you trust.
- Bad, because a server whose tools change at runtime keeps the tools listed at apply until the next apply.
- Neutral: an agent on such a server gets a new version when its tool list changes, as for any agent change.

### Confirmation

- Golden transcripts for `managed-agents/self-hosted` cover an agent with a stdio-only server: the worker image, the declared custom tools, `allowTools`, and the tool list recorded in state.
- Unit tests cover transport selection on both placements, tool name and schema limits, the approval error and the credential conflict.
- A worker test starts a stdio server through the MCP SDK and answers a custom tool call against a fake session, without Docker or the network.
- `SOLARIA_IT_MANAGED_AGENTS=1` gains a self-hosted case with a stdio server, run only with the maintainer's go-ahead.
- `docs/providers/managed-agents.md` and ADR 0005's transport table follow.

## Pros and cons of the options

### The self-hosted worker runs the stdio servers and serves their tools as custom tools

- Good, because it's a documented Managed Agents pattern, and the Go SDK already has it: `EnvironmentWorkerOptions.ToolsFunc` and `mcp.NewBetaTools`.
- Good, because Anthropic never connects to the server, so there's nothing to expose or authenticate.
- Bad, because tools must be declared up front, so Solaria has to list them.
- Bad, because permission policies don't cover custom tools.
- Bad, because it does nothing for `cloud`.

### A stdio-to-HTTP bridge container reached through an MCP tunnel

- Good, because it works on `cloud` as well, and the agent references a plain URL.
- Bad, because MCP tunnels are a research preview behind an access request, carried over Cloudflare, with no availability commitment.
- Bad, because the user deploys and operates the tunnel stack (cloudflared and Anthropic's proxy) and its certificates, and the tunnel doesn't authenticate to the server, so the bridge needs auth of its own.
- Neutral: users can already do this by hand and declare the URL in `remotes`. It's an Unscheduled item to make it easier.

### A stdio-to-HTTP bridge with a public endpoint

- Good, because it works on any host that takes a URL.
- Bad, because it exposes a tool server to the internet, which is the infrastructure decision ADR 0005 refused to hide inside a provider.

### Keep failing plan, and require servers with `remotes`

- Good, because there's nothing to build.
- Bad, because many servers ship only as stdio packages, and the adopter needs them on `self-hosted`.

## More information

- [Custom tools in self-hosted sandboxes](https://platform.claude.com/docs/en/managed-agents/self-hosted-sandboxes-custom-tools), including "Wrap an MCP server as custom tools" and its limits.
- [Self-hosted sandboxes and MCP tunnels](https://platform.claude.com/docs/en/managed-agents/self-hosted-sandboxes#self-hosted-sandboxes-and-mcp-tunnels)
- [MCP tunnels](https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/overview), the research preview the tunnel option depends on.
- ADR 0005 (transports), ADR 0012 and ADR 0020 (one worker per Environment), ADR 0016 (vaults).
