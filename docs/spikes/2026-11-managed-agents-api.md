# Spike: Claude Managed Agents API for the `managed-agents` provider

- **Date:** 2026-11-04
- **Beta header:** `managed-agents-2026-04-01`
- **Question:** What exactly does the provider send, read and delete? Does the planned mapping (ADRs 0004–0009) hold?
- **Method:** docs only, no API calls. Docs base: `https://platform.claude.com/docs/en/`. Items marked UNVERIFIED weren't documented.

## Summary

| Planned | Result |
|---|---|
| Agent card → `model`, `system`, `tools`, `mcp_servers`, `skills`, `metadata` | ✓ holds, with normalization (below) |
| Environment → cloud `packages` + `networking` | ✓ holds; `resources` can't be mapped (fixed limits) |
| `allowTools` → `mcp_toolset.configs[].enabled` | ✓ |
| `permissions.ask` → `permission_policy: always_ask` | ✓ native, per tool |
| MCP auth via `secretRef` on McpServer | ✗ on this host: credentials live in **vaults attached per session**, never on the agent |
| MCP from self-hosted workers | ✗ Anthropic opens MCP connections, not the worker: remote-only on both placements |
| Rollback by re-pointing to an old version | ⚠ only for sessions that pin `{id, version}`; "latest" can't be reverted |
| Destroy | ⚠ agents can only be **archived** (irreversible); environments can be deleted when unreferenced; skills deleted |
| Agent version in traces | ✗ spans carry `gen_ai.agent.id` and `name`, not the version |
| **Prior art** | ⚠ **`ant apply`**: Anthropic's own declarative apply for this host |

## Agents

- **Endpoints:**
  - `POST /v1/agents` (create);
  - `POST /v1/agents/{id}` (update; a POST, not a PATCH);
  - `GET /v1/agents/{id}?version=N`;
  - `GET /v1/agents` (`include_archived`, paging);
  - `GET /v1/agents/{id}/versions`;
  - `POST /v1/agents/{id}/archive`.

  There is **no delete**.
- **Fields:**
  - `name` (1–256 characters), `description` (≤2048), `system` (≤100k).
  - `model`: a string, or `{id, effort, speed, inference_geo}`.
  - `tools` (≤256 entries across all toolsets), `skills`, `mcp_servers` (≤20).
  - `multiagent` (`multiagent_20261001`), `metadata`.
  - `execution_identity` (new).
- **Update semantics:**
  - Sending `version` gives optimistic concurrency: a 409 on mismatch, even when nothing changed.
  - Omitted fields are kept.
  - `tools`, `mcp_servers` and `skills` are **replaced whole**.
  - Within `model`: an omitted `effort` is kept when the `id` is unchanged; an omitted `inference_geo` is cleared.
  - An update that changes nothing creates no new version.
  - Archived agents reject updates.
- **Metadata:** ≤16 pairs, keys ≤64 characters, values ≤512. Updates **merge per key**, and `null` deletes a key. Allowed characters: UNVERIFIED.
- **Archive:** irreversible and read-only. Running sessions continue, but new sessions can't reference the agent.

## Tools, permissions, MCP

- **Built-in toolset:** `agent_toolset_20260401` contains `bash`, `read`, `write`, `edit`, `glob`, `grep`, `web_fetch`, `web_search`. Configuration:
  - `default_config: {enabled, permission_policy}`;
  - per-tool overrides in `configs: [{name, enabled, permission_policy}]`;
  - `web_*` tools also take `allowed_domains` and `blocked_domains`.
- **Permission policies:** `always_allow`, `always_ask`, `auto`. The built-in toolset defaults to `always_allow`; **MCP toolsets default to `always_ask`**. So the provider must set policies explicitly.
- **MCP servers:** only `{type: url, name, url}`. Transport is remote streamable HTTP, with automatic fallback for SSE-only servers. Private servers go through MCP tunnels.
- **Pairing rule:** every `mcp_servers` entry needs a matching `{type: mcp_toolset, mcp_server_name, default_config, configs}`, and vice versa, or the request fails.
- **Auth:** per session, via `vault_ids` (`mcp_oauth` or `static_bearer` credentials, matched by server URL). Agents never hold secrets, and changing vaults on a running session isn't supported.
- **Self-hosted:** "Anthropic, not your worker, connects to the agent's MCP servers." To use a local server, wrap it as custom tools served by the worker.

## Skills

- **Create:** `POST /v1/skills` (multipart `files[]` with `SKILL.md` in one top-level folder; zip is also accepted).
- **New version:** `POST /v1/skills/{id}/versions`.
- **Delete:** `DELETE /v1/skills/{id}` or `/versions/{v}`. There's no update, no archive and no metadata.
- **Agent reference:** `{type: custom, skill_id, version}`. `version` defaults to `latest`. Whether `latest` is frozen when the agent is saved is UNVERIFIED, so **the provider always pins an explicit version.**

## Environments

- **Endpoints:** `POST /v1/environments`, `POST /v1/environments/{id}` (update in place, omitted fields kept), `GET`, `POST …/archive`, `DELETE` (only when no session references the environment).
- **Versions:** environments are **not versioned**.
- **Cloud config:**
  - `packages`: `{apt, cargo, gem, go, npm, pip}`, each a string array with native pin syntax (`sqlalchemy==2.0.30`, `hyperfine@1.18.0`).
  - `networking`: `{type: unrestricted}`, or `{type: limited, allowed_hosts[], allow_package_managers, allow_mcp_servers}`.
- **Rules:**
  - `limited` networking with packages **requires** `allow_package_managers: true` (otherwise a 400).
  - `limited` without `allow_mcp_servers` rejects sessions whose MCP hosts aren't in `allowed_hosts`.
  - Omitting `networking` means **unrestricted**.
- **Resources:** fixed at ≤8 GB memory, ≤10 GB disk, x86_64, **Ubuntu 24.04 LTS** (the handover's 22.04 is outdated).
- **Self-hosted:** `config: {type: self_hosted}`. The **environment key can only be generated in the Console.**
  - Worker variables: `ANTHROPIC_ENVIRONMENT_ID`, `ANTHROPIC_ENVIRONMENT_KEY`.
  - `ant beta:worker poll` flags: `--environment-id`, `--environment-key`, `--workdir`, `--on-work`, `--max-idle` (60 s).
  - Modes: always-on, webhook-triggered (SDK only) and one sandbox per session.
  - Session resources: only `memory_store`; `file` and `github_repository` return 400.

## Sessions and observability

- **Pinning:** `agent: {type: agent, id, version}`. Without `version`, a session gets the latest version **at creation**.
- **Traces:** spans carry `gen_ai.agent.id`, `gen_ai.agent.name`, `anthropic.session.id`, `anthropic.session.environment.id`. **There's no agent version attribute,** and telemetry destinations accept only headers (no custom attributes; UNVERIFIED that no other way exists).
- **Correlation:** joining a trace to a definition needs Solaria's state: host ID + version → `resolvedDigest`.

## Go SDK

- **Module:** `github.com/anthropics/anthropic-sdk-go/v2` v2.5.0 (2026-11-02).
- **Calls:**
  - `client.Beta.Agents.{New, Get, Update, List, Archive}` and `.Versions.List`;
  - `client.Beta.Environments.{New, Get, Update, List, Delete, Archive}`;
  - `client.Beta.Skills.{New, Get, List, Delete}` and `.Versions.*`.
- **Prebuilt worker:** `lib/environments.NewEnvironmentWorker` (verified at v2.5.0). The self-hosted worker container can therefore be a small Go program shipped with Solaria instead of the `ant` CLI.

## Prior art: `ant apply`

The `ant` CLI (v1.30.0 or later; **1.21.0 is installed here**) has a declarative workflow for this host's resources: agents, environments, skills, memory stores, deployments and vaults ([docs](https://platform.claude.com/docs/en/cli-sdks-libraries/cli/apply)).

- **File format:** `agents/<name>.md`, with frontmatter holding the agent fields and the body as the system prompt. Kind is inferred from a `type` field, then the folder, then the file name.
- **Plan and approval:** an interactive plan, a `d`etails view with per-field diffs, and `--dry-run`.
- **Lockfile:** `claude-lock.json` is committed and holds `origin` (base URL, organization, workspace) and, per file, `{kind, id, version, hash, remote_hash}`.
- **Removing resources:** deleting a file leaves the resource and warns; `--prune` archives it (or deletes it, for skills).

**What this means for Solaria:**

1. **Positioning:** `ant apply` covers *this one host*. Solaria's value is the neutral card, the same identity across hosts, and multiple providers. The README should acknowledge `ant apply` and say this plainly.
2. **Borrow `remote_hash`:** storing a hash of the host's *normalized* response next to the desired hash gives cheap drift detection (did someone change it in the Console?). Add `remoteHash` to the state entry.
3. **Provider implementation (decision needed):**
   - **(a) Call the API directly** through the Go SDK. Full control over metadata, explicit skill pinning, version history and normalization; unit-testable with `httptest`; no dependency on the `ant` version.
   - **(b) Render `ant apply` files and run `ant apply`.** Less code, and it tracks Anthropic's schema. But it depends on an external CLI version and parsing its output, needs a second lockfile per environment, and gives less control over rollback and metadata.

   Recommendation: (a), and keep (b) as a possible `solaria export --format ant` later.

## Effects on the design

- **Drift normalization:** fill server defaults before diffing: `effort`, `speed`, `default_config`, the inferred `type` on config entries, `description: null`, `metadata: {}`. Pin skill versions explicitly.
- **Full-replace arrays:** always send complete `tools`, `mcp_servers`, `skills`. Send `null` for removed metadata keys.
- **MCP credentials** don't belong on the agent for this host. In v0 the provider reports which vault credentials sessions need, and checks capabilities: a `secretRef` header on a remote warns "provide through a vault at session start." A host-backed `Vault` resource comes later.
- **Networking coupling:** the environment's `limited` networking must allow the agents' MCP hosts. The provider sets `allow_mcp_servers` when agents bound to the environment use MCP, or plan fails with the missing hosts.
- **Destroy:** archives agents (irreversible; plan shows it as `-/archive`), deletes skills, and deletes the environment, or archives it if sessions still reference it.
- **Rollback:** re-applying old content creates version N+1 with the same `resolvedDigest`. The binding's `history` maps both versions to that digest, and consumers that pin sessions read the current binding from state.
