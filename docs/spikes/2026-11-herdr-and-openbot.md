# Spike: Herdr and OpenBot as Solaria hosts

- **Date:** 2026-11-10
- **Question:** Should [Herdr](https://herdr.dev) and [OpenBot](https://openbot.run) be providers, placements or neither (ADR 0006)? What would an integration need, and what would people do with it?
- **Method:** docs and source READMEs only. Nothing was installed or run. Items marked UNVERIFIED weren't documented.

## Summary

| Question | Result |
|---|---|
| Herdr: provider? | ✗ It defines no agent and runs no loop. It runs any command in a persistent terminal. |
| Herdr: placement? | ⚠ Later, as a remote form of the `process` placement (ADR 0014). First, it's a **caller** of Solaria's run command. |
| OpenBot: provider? | ✓ A host that runs agents from a definition, like Managed Agents. Placements `desktop` and `server`. |
| OpenBot: can Solaria write the definition? | ✗ Only partly. No API creates an agent or changes its instructions or MCP servers. |
| OpenBot: can Solaria detect drift? | ✗ The local API doesn't return instructions, skills or MCP servers. |
| OpenBot: hermetic? | ✗ Agents also read user-level skill folders, which the digest doesn't cover. |

## Herdr

### What it is

- A native app (macOS, Linux, Windows, phone) over **herdr-core**, a small server on each machine. Programs run in terminals that live in herdr-core, so they keep running when the app quits or updates. A reboot stops them, and Herdr doesn't restart them.
- Other machines join over SSH: `hc machine add box@my-server`, then `hc run --on my-server --bg claude`.
- The `hc` command:
  - `hc run [--bg] [--name <n>] [--on <machine>] <cmd>` starts a program;
  - `hc ls`, `hc attach`, `hc wait`, `hc read --lines N`, `hc send <name> "<text>"`;
  - `hc --skill` prints the manual for agents;
  - `hc schema` prints the API as OpenRPC, with streams and events.
- **Agents run agents:** an agent starts a helper with `hc run --bg`, tells it to report with `hc send $HC_TERMINAL_ID`, and the report arrives in the agent's input.
- **Status** comes from the program itself through OSC 7501, an open escape sequence (`printf '\e]7501;state=blocked:app=my-agent\e\\'`). No screen scraping, and no list of known agents.
- Apache 2.0: `herdrdev/herdr-app`, `herdrdev/herdr-core`, `herdrdev/herdr`. The older Herdr TUI is a separate program with its own server.

### Fit

ADR 0006 defines a provider as whoever runs the agent loop and a placement as where its sandbox lives. Herdr does neither of the first: the loop is whatever program it starts. It does decide where that program lives (this machine or an SSH machine), and keeps it alive.

ADR 0002 says Solaria records how to start an agent and the caller starts it. Herdr is a good caller, so the integration starts there.

### Integration, cheapest first

1. **No code.** Wrap the recorded run command: `hc run --bg --name reviewer <run command>`. With the `-o json` invocation block that ADR 0014 proposes, a script or an agent can build this without reading state.
2. **The launcher reports OSC 7501.** The `claude-agent-sdk` launcher sends `state=working` when the query starts and `state=done` when it ends, so a Solaria agent shows its status in Herdr's sidebar and on the phone. The launcher's stdout carries the one JSON result, so the sequence goes to stderr, and only when stderr is a terminal. `blocked` doesn't arise: unattended runs deny `permissions.ask` tools rather than wait.
3. **A `herdr` placement.** `process`, on a Herdr machine:
   - plan checks that the machine exists (`hc machine ls`) and that the runtime is there;
   - apply copies the bundle to the machine and records `hc run --on <machine> …` as the run command;
   - read checks that the bundle is still there;
   - the managed settings check of ADR 0014 runs on that machine, not this one.

   herdr-core's API has no file copy that the docs mention (UNVERIFIED), so the copy would use SSH directly.

### What `process` isolation means here

The `process` placement passes only allowlisted variables and a minimal `PATH`, so `HC_TERMINAL_ID` and `hc` don't reach the agent. A Solaria agent therefore can't start helpers or report through `hc` itself. That's the intended isolation and needs no change, because the wrapper outside the launcher can report:

```sh
hc run --bg --name review sh -c '<run command> -p "review the diff" | hc send "$HC_TERMINAL_ID"'
```

An agent that should orchestrate through `hc` would need an explicit opt-in: `hc` on its `PATH`, `HC_TERMINAL_ID` passed through, and probably Herdr's manual as a skill.

## OpenBot

### What it is

- A desktop app (macOS, Windows, Linux, phone, web client) for persistent agents. Each agent has a name, instructions, an avatar, a workspace (`~/OpenBot/Agents/<agent-id>`), skills, routines and memories.
- The loop runs in a provider CLI that OpenBot pins and starts: Claude Code, Codex App Server, Grok, OpenCode, Gemini, Cursor, Cline, Kimi, Devin, Copilot, Mistral Vibe, Hermes through ACP, or a custom ACP agent or OpenAI-compatible endpoint.
- Agents message each other, hand off tasks and share channels with people. Workflows combine agents, routines and approval steps.
- **Headless server:** `install-server.sh` on Ubuntu with systemd, or `ghcr.io/nightly-labs/openbot` in Docker, signed in with `openbot login`. The desktop and phone apps connect to it.
- Agents run with `danger-full-access` and `approvalPolicy: never` by default. A per-agent access mode ("Ask first") exists, with tool consent for MCP tools.
- Source is public under the PolyForm Noncommercial 1.0.0 license.

### Fit

OpenBot runs agents from a definition it stores, as Managed Agents does, so it's a provider. Its placements would be:

- `desktop`: the app on the machine that runs `solaria apply`;
- `server`: a headless install, its Docker image, or an OpenBot hosted server.

### What Solaria can write today

| Solaria field | OpenBot path | Notes |
|---|---|---|
| `model.preferred`, provider, effort | Local scripts API, `POST /v1/agents/:agentId/settings` | Only for agents with **Local scripts** turned on, which only a person can do. A provider or account change waits until the agent is idle (`409`). |
| Create an agent with instructions, skills, routines | Import ZIP: `openbot-import.json` plus `agents/<key>/` | A documented contract with a `version` (only optional fields are added). It's chosen in **Server settings → Import**, not through an API, and it only creates. |
| Instructions, after creation | None | Only the app, or the agent tool `openbot.update_profile`. |
| Skills | `<workspace>/.claude/skills/` and `.agents/skills/` | OpenBot lists these as workspace skills and never writes them. They don't count toward the agent's skill limit. |
| MCP servers | Server settings | Shared by the server's agents, not set per agent. No API. |
| `tools.builtin`, `permissions.ask` | Access mode | No API. |
| Destroy | None | |
| Read | `GET /v1/agents` | Returns `id`, `name` and routines only. |

The Local scripts API listens on `127.0.0.1` with a new port and token at each start, written to `<userData>/automation/{url,token,headers}` with mode `0600`. A provider would read those files each time, as `openbot-cli` does. On a `server` placement that means running on the server as its OS user. Calling it from another machine is UNVERIFIED.

### Where it falls short

- **Create-only.** A first apply could write the import ZIP and stop with "import this file", as the first apply of `managed-agents/self-hosted` stops for the environment key. After that, instructions and MCP changes have nowhere to go.
- **No drift.** OpenBot agents can change their own profile through `openbot.update_profile`, which is the strongest reason to manage them from git, but nothing reads the profile back.
- **Not hermetic.** Claude Code agents also read `~/.claude/skills/`, Codex and others read `~/.agents/skills/`, and Hermes agents read `$HERMES_HOME`. Plan should warn that the digest doesn't cover everything the agent sees, as ADR 0014 warns for managed settings.

A full provider needs an upstream route that reads and writes an agent's profile (instructions, skills, MCP servers, access), with a version for optimistic concurrency. The local API already has an opt-in model (`agent-host-settings-v1`) it could extend. That's a question for the OpenBot maintainers.

Solaria may call OpenBot's API under its license, but must not copy OpenBot's code into Solaria (Apache 2.0).

## Scenarios

1. **Compare runtimes in Herdr.** Run one card as `claude-agent-sdk/process`, `gitagent/docker` and `managed-agents/self-hosted`, one Herdr tab each, and compare the JSON results: cost, turns, SDK version. This is the first decision driver of ADR 0006.
2. **A pinned reviewer as a helper.** A Claude Code session in Herdr hands a review to `reviewer` at the digest `solaria.lock` pins for staging, and gets the result back through `hc send`. The helper is a versioned, hermetic definition instead of an ad-hoc `codex "review my diff"`.
3. **Always-on agents on a VPS.** Apply to a Herdr machine; the agents keep running while the laptop sleeps and are checked from the phone. Needs the `herdr` placement.
4. **An OpenBot team in git.** Each teammate is an Agent in the repository, instruction changes go through pull requests, and `solaria plan` shows when someone, or the agent itself, changed a profile in the app. Needs the profile route.
5. **Promotion across hosts.** One card on OpenBot `desktop` for one person, OpenBot `server` for a team channel, and Managed Agents in production, with the lock file pinning each stage.
6. **Starting from an existing OpenBot server.** Turn an instance-transfer ZIP (`openbot-transfer.json`: profile, provider, model, routines, skills) into Agent files. This is the `Import` that ADR 0006 leaves for later.

## What this changes

Nothing yet. It proposes, in order:

1. OSC 7501 status from the ADR 0014 launcher (an amendment to ADR 0014).
2. A `herdr` placement, once a remote `process` is needed (a new ADR).
3. An `openbot` provider, once OpenBot has a profile route (a new ADR, after asking upstream).

## Sources

- Herdr: [herdr.dev](https://herdr.dev), [herdr-core](https://herdr.dev/core/), [guide](https://herdr.dev/guide/), [the guide for agents](https://github.com/herdrdev/herdr-app/blob/master/defaults/AGENTS.md).
- OpenBot: [openbot.run](https://openbot.run), [README](https://github.com/nightly-labs/openbot#readme), in `docs/`: [self-hosted servers](https://github.com/nightly-labs/openbot/blob/main/docs/self-hosted-server.md), [local scripts API](https://github.com/nightly-labs/openbot/blob/main/docs/automation.md), [agents](https://github.com/nightly-labs/openbot/blob/main/docs/architecture/agents.md), [skills and plugins](https://github.com/nightly-labs/openbot/blob/main/docs/architecture/skills-and-plugins.md); [importing Hermes agents](https://openbot.run/guides/import-hermes-agents).
