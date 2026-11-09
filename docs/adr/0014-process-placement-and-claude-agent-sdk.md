---
status: proposed
date: 2026-11-08
decision-makers: Renan Tarouco da Fonseca
---

# Local agent loops: a hermetic `process` placement and the `claude-agent-sdk` provider

## Context and problem statement

Many teams run agents on a local agent loop rather than on a hosted service: the Claude Agent SDK, or CLIs such as pi and OpenCode. Solaria's providers today build a container (`gitagent`) or call an API (`managed-agents`), so these teams can't use Solaria at all. Two questions follow from ADR 0006:

- Where does such a loop run? Often as a plain process on the developer's or CI machine, which needs a placement besides `docker`.
- What does `apply` produce for the Claude Agent SDK? Unlike gitagent, it's a library in TypeScript and Python, not a program with a config directory, so there is nothing to point at.

Whichever the answer, the run must be **hermetic**. A local process sees everything its user has: settings files, skills, plugins, `CLAUDE.md` and `AGENTS.md` up the directory tree, stored logins, and inherited `CLAUDE*` and `ANTHROPIC*` variables. Teams that run the SDK themselves report needing several fixes before none of that leaked in. A definition whose digest says one thing while the run picks up the user's own skills isn't the definition Solaria applied.

## Decision drivers

- What runs is exactly the definition the digests describe, on every machine (ADR 0008).
- Every caller gets isolation for free, rather than each one getting it right on its own.
- Solaria still never runs an agent loop (ADR 0002).
- The same card works on `process`, `docker` and Managed Agents, and host limits show at plan time.
- A pinned older version stays runnable while a newer one runs elsewhere (ADR 0013).

## Considered options

- A bundle per digest plus a launcher that Solaria generates.
- An options file only, loaded by each caller's own runner.
- Render arguments and settings for the `claude` CLI in headless mode instead of the SDK.

## Decision outcome

Chosen option: "A bundle per digest plus a launcher that Solaria generates", because Solaria then owns isolation in one place, and the loop still runs inside the SDK, not in Solaria.

### The `process` placement

A new placement for providers whose loop runs as a local process. Every run gets:

- **An empty environment plus an allowlist:** a minimal `PATH`, the variables the provider needs, and the secrets the environment declares, by name. Nothing else from the caller's environment reaches the loop. This is an allowlist, not a blocklist of known leaks, so a variable nobody thought of can't leak either.
- **A scratch `HOME` and config directory,** created for the run and removed after it, so user-level settings, skills, plugins, stored logins and memory are never found.
- **A working directory** that is the workspace the caller names (the current directory by default). Files in it are the agent's to work on, but the runtime is told not to load configuration from it or its parents.

How a provider enforces this depends on the runtime. A library runtime does it in the launcher (below); a CLI runtime, such as pi or OpenCode later, gets a generated wrapper. Shared code lives in `internal/providers/process/`, next to `internal/providers/container/`.

The guarantee has a documented limit. A machine can carry **managed settings** that every Claude Code process obeys, wherever it runs: `/Library/Application Support/ClaudeCode/managed-settings.json` on macOS, `/etc/claude-code/managed-settings.json` on Linux, the Windows registry, or MDM. When a session authenticates with an organisation's credential, the server can push managed settings too. Neither can be turned off from inside the process. Plan warns when it finds a managed settings file on a `process` placement. The `docker` placement doesn't see the host's files, so it's the answer when that matters.

### The `claude-agent-sdk` provider

Placements: `process` and `docker`.

**Apply writes a bundle** per agent and environment under `.solaria/claude-agent-sdk/<env>/<agent>/<buildDigest[:12]>/`, gitignored like the rest of `.solaria/`. The build digest covers the `resolvedDigest`, the launcher language and the SDK version, since the last two aren't part of the definition. The bundle holds:

- `options.json`: the SDK options the definition maps to;
- the instructions, as one system prompt that replaces the SDK's preset;
- skills, as a local plugin directory (`plugin/skills/<name>/SKILL.md`), loaded through the SDK's `plugins` option, which works with every setting source off;
- the launcher, `launch.mjs` or `launch.py`;
- the SDK, installed at an exact version (`package.json` with its lock file, or a virtual environment).

A successful apply removes the previous bundle, as `gitagent` removes the previous image tag.

**Launcher language:** TypeScript or Python, chosen per environment with `providers.claude-agent-sdk.runtime: node | python` (default `node`). Plan checks that the runtime is present: Node 18.14 or later, or Python 3.10 or later. Both SDK packages bundle the Claude Code binary, so nothing else needs installing.

**SDK version:** the provider pins a tested version of each package. An Environment can override it with `providers.claude-agent-sdk.version`. The binding records the SDK version used (outputs `sdkVersion` and `runtime`), because an eval measures a model inside a harness, and the harness version matters.

**What the launcher does on every run:**

1. Creates the scratch `HOME` and `CLAUDE_CONFIG_DIR` and builds the child environment from the allowlist. It does this itself rather than relying on the SDK's `env` option to replace the inherited environment. That is documented for TypeScript but not for Python, so the Python launcher clears `os.environ` before the SDK starts.
2. Sets `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`, `CLAUDE_CODE_DISABLE_CLAUDE_MDS=1`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and `ENABLE_CLAUDEAI_MCP_SERVERS=false` in the child environment.
3. Calls the SDK with `settingSources: []`, set explicitly. The SDK's default loads user, project and local settings, so leaving it out would load everything this ADR keeps out. It also passes a strict MCP config containing only the card's servers.
4. Reads the task from `-p "<task>"`, or from stdin when there's no `-p`.
5. Enforces `limits.timeout` by aborting the query, as the SDK has no timeout of its own.
6. Prints one JSON object on stdout: the SDK's result (`subtype`, `result`, `structured_output`, `usage`, `total_cost_usd`, `num_turns`, `session_id`), plus `logicalId`, `resolvedDigest` and `sdkVersion`. It exits non-zero when the result is an error, and still prints the result in that case.
7. Removes the scratch directories.

**How the definition maps** (details in `docs/providers/claude-agent-sdk.md` once it's built):

- `model.preferred` becomes `model`. The SDK takes one fallback model, so the first `fallback` entry becomes `fallbackModel`, and plan warns when there are more.
- `tools.builtin` is enforced with `disallowedTools` for the built-ins the card leaves out. `allowedTools` only approves tools in advance; it doesn't restrict them.
- Tools under `permissions.ask` are denied in unattended runs, as on `gitagent`. The `Approvals` capability is false, so plan warns.
- `limits.maxTurns` becomes `maxTurns`.
- MCP servers go into the strict MCP config: stdio packages run through `npx` or `uvx`, remotes over streamable HTTP or SSE.
- Model providers other than `anthropic` fail at plan time.

**Run command:** apply records it as the `run` output and prints it, as `gitagent` does. On `process` it's `node <bundle>/launch.mjs` or `python <bundle>/launch.py`, with the secret names it expects. On `docker` it's a `docker run` of an image built on the shared sandbox base (ADR 0007), with the same launcher and bundle inside and the workspace mounted at `/workspace`. `solaria status <agent> -e <env>` shows the same command, and `-o json` gives it as a documented `invocation` block, so callers never depend on the state format.

**ADR 0002 still holds:** Solaria writes the bundle and the launcher, and records how to start them. The caller starts them, and the loop runs inside the SDK.

### Consequences

- Good, because isolation is written and tested once, and every caller of a binding gets it.
- Good, because a locked older version keeps its own bundle on disk until it's replaced, so two stages can run two versions.
- Good, because the launcher is Node or Python, so `process` works on macOS, Linux and Windows alike, with no shell wrapper.
- Good, because the result is one JSON object with a fixed shape, so a caller can read the answer and its cost without knowing the SDK.
- Bad, because Solaria now ships launcher code in two languages, and tests both.
- Bad, because apply installs packages, so it needs network access to npm or PyPI, and takes longer the first time.
- Bad, because managed settings on the machine, or pushed by an organisation's server, can't be isolated on `process`. Plan warns, and `docker` is the answer.
- Neutral: skills loaded as a plugin are named `<plugin>:<skill>` inside the run. The plugin is named after the agent so names stay readable.

### Confirmation

- Golden tests of the rendered bundle and launcher for both languages, like `internal/providers/gitagent`.
- An opt-in integration test, `SOLARIA_IT_CLAUDE_AGENT_SDK=1`, that plants each kind of leak and checks that none reaches the run:
  - a user `settings.json` with a hook;
  - a user skill and plugin;
  - a `CLAUDE.md` and `AGENTS.md` in the workspace and its parent;
  - `.mcp.json` in the workspace;
  - `CLAUDE_*` and `ANTHROPIC_*` variables in the caller's environment.

  The test reads the session's init message and checks its tools, skills and MCP servers against the card.
- A unit test of the `process` helper's environment allowlist.

## Pros and cons of the options

### An options file only

- Good, because Solaria ships no launcher code.
- Bad, because every caller writes its own runner and has to get isolation right on its own, which is exactly the work teams report getting wrong.
- Bad, because the result format differs per caller, so a definition can't be swapped between hosts without changing the caller.

### The `claude` CLI in headless mode

- Good, because there's no launcher: the binding is an argument list.
- Bad, because isolation then rests on CLI flags. Bare mode skips the most, but it has no SDK equivalent and its effect on managed settings isn't documented.
- Bad, because it isn't the surface teams build on when they embed Claude in their own programs, so a definition tested this way isn't tested the way it will run.

## More information

- [ADR 0002](0002-control-plane-not-agent-framework.md): Solaria never runs the loop.
- [ADR 0006](0006-providers-and-placements.md): providers and placements.
- [ADR 0007](0007-portable-sandbox-is-packages.md): the sandbox base the `docker` image builds on.
- [ADR 0013](0013-lock-file-pins-digests.md): the lock file that keeps an older version pinned.
- Claude Agent SDK: [options (TypeScript)](https://code.claude.com/docs/en/agent-sdk/typescript), [options (Python)](https://code.claude.com/docs/en/agent-sdk/python), [what `settingSources` controls and what it doesn't](https://code.claude.com/docs/en/agent-sdk/claude-code-features), [plugins](https://code.claude.com/docs/en/agent-sdk/plugins), [secure deployment](https://code.claude.com/docs/en/agent-sdk/secure-deployment).
- Claude Code: [managed settings](https://code.claude.com/docs/en/managed-settings), [environment variables](https://code.claude.com/docs/en/env-vars).
- Not yet verified, to check in the integration test before acceptance:
  - whether the Python SDK merges `os.environ` into its `env` option;
  - whether a scratch `HOME` also moves the Anthropic profile directory (`~/.config/anthropic`);
  - the full TypeScript result message fields.
