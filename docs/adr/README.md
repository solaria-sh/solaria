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
| [0011](0011-managed-agents-provider-calls-the-api.md) | The `managed-agents` provider calls the API directly | accepted |
| [0012](0012-environment-level-host-objects.md) | Environment-level host objects | accepted |
| [0013](0013-lock-file-pins-digests.md) | A committed lock file pins each environment's digests | proposed |
| [0014](0014-process-placement-and-claude-agent-sdk.md) | Local agent loops: a hermetic `process` placement and the `claude-agent-sdk` provider | proposed |
| [0015](0015-project-scaffolding-and-editor-support.md) | Project scaffolding and editor support | proposed |

ADRs 0005–0009 were revised on 2026-11-04 with findings from the [M1 spikes](../spikes/). ADR 0008 records the digest input format since M3.
