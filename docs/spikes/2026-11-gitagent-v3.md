# Spike: gitagent v3 as the first container runtime

- **Date:** 2026-11-04
- **Question:** Can the `gitagent` provider (ADR 0006) render an Agent card and run it in a container, with MCP, tool narrowing, approvals, per-environment config and telemetry?
- **Pinned source:** [`open-gitagent/gitagent@f4a309c`](https://github.com/open-gitagent/gitagent/tree/f4a309cd600b877177c17ac789e83bd7355d0383) (workspace version 3.0.0)
- **Method:**
  - Read the source.
  - Built a multi-stage image: `rust:1-bookworm` builder, then an `ubuntu:22.04` runtime with gitagent 3.0.0, 400 MB, native arm64.
  - Ran the image offline (`--network none`) with gitagent's `scripted:` model, so no credentials were needed.

## Summary

| Need | Result | How the provider does it |
|---|---|---|
| Install a pinned version | ⚠ v3 isn't released (latest release v2.2.0 is the TypeScript version; `install.sh` finds no v3 assets) | Build from a pinned commit for now; switch to a checksummed release download when v3 ships |
| Instructions, rules, skills | ✓ | Render into gitagent's **config layer**: `$HOME/.gitagent/prompts/system.md`, `prompts/rules.md`, `skills/<name>/SKILL.md` |
| Keep the workspace clean | ✓ only without `--dir`, in a non-empty folder | Run in the workspace as the working directory, never pass `--dir` |
| MCP stdio | ✓ verified (`python3` echo server) | `mcp_servers.<name>: {command, args, env}` |
| MCP streamable HTTP / SSE | ✓ in code; an unreachable server is **left out with a warning**, and the run continues | `{url, headers}`, `type: sse` for legacy |
| Secrets for MCP | ✓ `${VAR}` substituted when connecting; never in the prompt or records | `secretRef` → `${SOLARIA_SECRET_<NAME>}`, passed at `docker run` |
| `allowTools` | ✓ verified | `--allowed-tools`. MCP tools are named `<server>__<tool>` (e.g. `echo__say`) |
| Builtin tool narrowing | ⚠ the manifest's `tools:` list does **not** limit built-ins; `--allowed-tools` does | Always emit an explicit `--allowed-tools` list |
| `permissions.ask` | ⚠ `compliance` works only in `agent.yaml`, and is **ignored** in the config layer; with no terminal, approvals are denied anyway | Map `ask` tools to `--disallowed-tools` and warn at plan time that "ask" means "deny" in unattended runs |
| Environment overrides | ✓ `config/<env>.yaml` `model_override` verified | Not needed: Solaria resolves overrides before rendering (ADR 0008) |
| OTel identity attributes | ✗ resource has only `service.name` and `service.version`; `OTEL_RESOURCE_ATTRIBUTES` is ignored | `OTEL_SERVICE_NAME=<agent>` for now; propose `OTEL_RESOURCE_ATTRIBUTES` upstream |
| Deterministic tests | ✓ `--model scripted:<file>` | Opt-in Docker integration test needs no API key |
| Runtimes in the image | ⚠ `ubuntu:22.04` apt gives **Node 12** and **Python 3.10** | The base image must match the cloud sandbox's runtimes (see below) |

## Details

### Release status

- The Rust rewrite (3.0.0) was merged to `main` on 2026-11-03.
- The latest GitHub release is v2.2.0 (2026-08-20). Its assets don't include the `gitagent-<target>.tar.gz` and `SHA256SUMS` that `scripts/install.sh` downloads.
- npm's latest is also 2.2.0.
- Project signals: MIT license, 743★, 60 open issues, two main contributors (Lyzr), very active.

Building from source takes about 2 minutes natively. An emulated `linux/amd64` build would be much slower, so x86_64 parity should wait for release binaries.

### Where the definition lives

gitagent treats its working folder as an agent folder. `--dir <folder>`, or an empty working directory, gets **scaffolded**: gitagent writes `agent.yaml`, `SOUL.md`, `memory/` and `.gitignore`, then runs `git init` and commits (`crates/cli/src/main.rs`, `setup`).

Configuration is also read from a layered lookup: `--config`, then the project's `.gitagent/`, then `~/.gitagent/`, then `.claude/`.

Verified layout:

- The rendered definition goes in `$HOME/.gitagent/`:
  - `gitagent.yaml`: model, `max_turns`, `mcp_servers`;
  - `prompts/system.md` and `prompts/rules.md`;
  - `skills/`.
- gitagent runs inside a non-empty workspace without `--dir`.
- Result: identity, rules and skill descriptions all reach the prompt, MCP tools are offered, and the workspace keeps only what the agent itself writes.

This matters for the factory, whose workspace is a clone of the product repo.

### Unattended-run behavior worth knowing

- **Stop check:** `task_complete` is rejected until a verifying command runs after the last change (`stop_check` events). A scripted test has to end with a verification command, or assert on outputs instead of the exit code.
- **Exit codes:** 0 when a run ends, including when it runs out of turns or time; 2 for usage errors; 3 when the provider fails (a scripted run that runs out of turns exits 3); 130 when stopped.
- **Sandbox:** stays on inside the container (Landlock), restricting writes to the work, output and temp folders.

### Runtimes in the base image

Ubuntu's own packages are too old for MCP servers and for parity with the cloud:

| Runtime | ubuntu 22.04 apt (verified) | Cloud sandbox |
|---|---|---|
| Node | 12.22 | 20+ |
| Python | 3.10 | 3.12+ |

The cloud sandbox also preinstalls Go, Rust, Java 21+, Ruby, PHP and GCC 13+, on **Ubuntu 24.04** x86_64 (see the Managed Agents spike). The generated Dockerfile therefore starts from a Solaria base image that reproduces the cloud sandbox's preinstalled runtimes, then adds the card's `packages`. → ADR 0007 is updated.

## Follow-ups

- Propose `OTEL_RESOURCE_ATTRIBUTES` support to gitagent (`crates/cli/src/otel.rs`, `resource()`).
- Switch to release binaries once v3.0.0 is published, verifying `SHA256SUMS`.
- Later: an approval bridge through the SDK protocol's `approval` callback, so `ask` can mean "ask" again.
