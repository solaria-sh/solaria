# Architecture decision records

Each significant design decision is recorded here using [MADR](https://adr.github.io/madr/). Copy [template.md](template.md) to propose a new one.

| # | Decision | Status |
|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | accepted |
| [0002](0002-control-plane-not-agent-framework.md) | A control plane, not an agent framework | accepted |
| [0003](0003-go-apache-2-and-distribution.md) | Go, Apache-2.0 and distribution | accepted |
| [0004](0004-resource-model.md) | Resource model: envelope, kinds, references, provider passthrough | accepted |
| [0005](0005-mcp-server-follows-server-json.md) | McpServer follows the MCP Registry `server.json` shape | accepted |
| [0006](0006-providers-and-placements.md) | Providers and placements; "local" is an environment | accepted |
| [0007](0007-portable-sandbox-is-packages.md) | The portable sandbox is a package list | accepted |
| [0008](0008-agent-identity.md) | Agent identity: logical ID, two digests, host binding | accepted |
| [0009](0009-state.md) | State: local per environment, locking, backend interface | accepted |
| [0010](0010-promotion-policy-out-of-scope.md) | Promotion and autonomy policy are out of scope | accepted |
