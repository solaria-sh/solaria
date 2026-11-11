# `claude-agent-sdk` provider

The `claude-agent-sdk` provider runs each agent on the [Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview), in TypeScript or Python. It has two placements:

- `process` runs the agent loop and its tools as a local process: on your laptop, in CI, or wherever you start it.
- `docker` runs the same launcher in a container image built on the [Solaria sandbox base](../concepts.md#the-sandbox), with the workspace mounted. It's the placement that installs `sandbox.packages`, and that keeps the machine's managed settings out.

Solaria doesn't start either. `apply` writes a bundle with a launcher, builds the image on `docker`, and records the command that starts it.

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Environment
metadata: {name: local-sdk}
spec:
  provider: claude-agent-sdk
  placement: process
  sandbox:
    network: {mode: unrestricted}
```

**Needs:** on `process`, Node 18.14 or later, or Python 3.10 or later with `providers.claude-agent-sdk.runtime: python`; `plan` checks that it's installed. On `docker`, the `docker` CLI with BuildKit for `apply`, and nothing for `plan`. Either way, `apply` installs the SDK from npm or PyPI, so it needs network access the first time, and running the agent needs `ANTHROPIC_API_KEY`.

## What apply does

1. Renders a bundle into `.solaria/claude-agent-sdk/<environment>/<agent>/<id>/`, where `<id>` is the first 12 hex digits of the bundle's build digest. The digest covers every file in the bundle, so it changes with the definition, the runtime, the SDK version and Solaria's launcher.
2. Installs the SDK into the bundle at an exact version: `npm install` from a generated `package.json`, which writes `package-lock.json`, or a virtual environment (`venv/`) for Python, whose resolved versions go to `requirements.lock`. Downloads are cached in `.solaria/claude-agent-sdk/.cache/`.
3. Removes the agent's previous bundle.
4. Records the command that starts the launcher. `apply` prints it:

```console
$ solaria apply -e local-sdk
...
  Agent/hello: .solaria/claude-agent-sdk/local-sdk/hello version 3f9c1a0d2b7e (sha256:08ffaed7b158)
    run: node /path/to/project/.solaria/claude-agent-sdk/local-sdk/hello/3f9c1a0d2b7e/launch.mjs
```

`solaria status -e local-sdk -a hello` shows it again later, with the variables it needs, and `-o json` gives it as an `invocation` for programs (see [Starting an agent](../concepts.md#starting-an-agent)). Run it from the folder the agent should work in. The task goes after it with `-p`, or on stdin:

```bash
node .solaria/claude-agent-sdk/local-sdk/hello/3f9c1a0d2b7e/launch.mjs -p "Say hello."
```

The bundle holds:

| File | |
|---|---|
| `options.json` | The SDK options the definition maps to, with secrets as references |
| `system-prompt.md` | The instructions, as one system prompt that replaces the SDK's own |
| `plugin/` | The skills, as a local plugin named after the agent |
| `isolation.json`, `isolate.mjs` or `isolate.py` | What keeps the run hermetic, below |
| `launch.mjs` or `launch.py` | The launcher |

## The `docker` placement

```yaml
spec:
  provider: claude-agent-sdk
  placement: docker
  sandbox:
    packages: {apt: [ripgrep]}
```

`apply` writes the same bundle, plus a `Dockerfile`, and builds `solaria/<environment>/<agent>:<id>` from it:

1. The sandbox stages every Solaria image shares: the Solaria sandbox base, which has Node, Python and uv, or `sandbox.image`, then the environment's packages.
2. The SDK at its exact version, installed in `/opt/solaria/agent`. Nothing in this layer depends on the agent, so agents on the same SDK version and sandbox share it.
3. The bundle, the only layer a card change rebuilds.

The image is labeled with the logical ID, both digests, the runtime and the SDK version. A successful apply removes the previous image and build context, and records a `docker run` command; the task goes after it:

```bash
docker run --rm -i --platform linux/amd64 --memory 8g --volume "$PWD":/workspace \
  --env ANTHROPIC_API_KEY solaria/local-sdk-docker/hello:3f9c1a0d2b7e -p "Say hello."
```

The secrets come from the environment's dotenv file (`--env-file`) or the caller's environment (`--env NAME`). Inside, the launcher isolates the run as on `process`, but keeps the image's own `PATH`, so the run finds what `sandbox.packages` installed. The first build installs the sandbox base and takes several minutes; later builds reuse Docker's cache.

A `sandbox.image` replaces the Solaria sandbox base, so it must have the runtime (Node 18.14 or later with npm, or Python 3.10 or later with venv) and `npx` or `uvx` for stdio MCP servers. Plan warns about what it needs.

## Every run is hermetic

A local process sees everything its user has: settings, skills and plugins in the home folder, `CLAUDE.md` files up the tree, stored logins, and every variable in the shell. The launcher keeps all of that out, so what runs is exactly the definition its digest describes:

- **The environment starts empty.** The run gets a minimal `PATH`, locale, proxy and certificate settings, `ANTHROPIC_API_KEY`, and the secrets its MCP servers name, or, when the agent lists [vaults](../concepts.md#credentials-and-vaults), exactly their credentials, with an `env` credential set under its variable's name. Nothing else from your shell, or from the dotenv file, reaches it. A missing secret, or a missing `npx` or `uvx` for a stdio server, fails the run before it starts.
- **`HOME`, `CLAUDE_CONFIG_DIR` and the temp folder are scratch directories,** created for the run and removed after it.
- **The SDK loads no settings files** (`settingSources: []`) and only the card's MCP servers (`strictMcpConfig`). Claude Code's auto memory, `CLAUDE.md` loading, claude.ai connectors and nonessential traffic are off.

**Managed settings are the exception on `process`.** A machine can carry settings that every Claude Code process obeys, and no process can turn them off: `managed-settings.json`, `managed-settings.d/` and `managed-mcp.json` in `/Library/Application Support/ClaudeCode/` (macOS), `/etc/claude-code/` (Linux) or `C:\Program Files\ClaudeCode\` (Windows), a macOS configuration profile, or the Windows policy registry. Plan warns about each one it finds. A container doesn't see them, so `docker` is the placement that keeps them out. Settings an organization's server pushes when a session signs in can't be seen in advance on either placement.

## What a run prints

The launcher prints one JSON object on stdout: the SDK's result plus the agent's identity.

```json
{"logicalId": "Agent/hello", "environment": "local-sdk", "resolvedDigest": "sha256:08ffaed7b158...", "runtime": "node", "sdkVersion": "0.3.319",
 "subtype": "success", "is_error": false, "result": "Hello!", "usage": {...}, "total_cost_usd": 0.0012, "num_turns": 1, "session_id": "..."}
```

| Exit code | `subtype` | |
|---|---|---|
| 0 | `success` | The run succeeded. |
| 1 | `error_max_turns`, `error_max_budget_usd`, `error_during_execution`, ... | The SDK's own error, or `success` with `is_error` set. |
| 1 | `error_timeout` | The run took longer than `limits.timeout` and was aborted. |
| 1 | `error_max_structured_output_retries` | The agent has `output.schema` and gave no answer that matched it. |
| 2 | `error_launch` | The run couldn't start: no task, a missing secret, binary or API key, or the SDK isn't installed. |

`errors` says what went wrong.

When the agent has `output.schema`, `structured_output` holds the answer: a JSON object the SDK has checked against the schema. The SDK re-prompts the agent when an answer doesn't match. It treats the `format` keyword as an annotation and doesn't check it, so plan warns about schemas that use it.

## How the definition maps

| Solaria | Claude Agent SDK |
|---|---|
| `instructions` | `systemPrompt`, one custom prompt. Without instructions, the SDK's minimal prompt. |
| `model.preferred` | `model` |
| `model.fallback` | `fallbackModel`, as a comma-separated chain. Claude Code caps a chain at three models, so plan warns when there are more than two fallbacks. |
| `model.effort` | `effort` |
| `tools.builtin` | `tools`, the exact list of built-in tools: `shell` is `Bash`, `read` is `Read`, `web_fetch` is `WebFetch`, and so on |
| `tools.mcp` | `mcpServers`. A remote is used when the server has one, over streamable HTTP or SSE; otherwise `npx` or `uvx` runs the package. |
| `allowTools` | `allowedTools` lists `mcp__<server>__<tool>` for each. The server's other tools are denied when called, but the agent still sees them, so plan warns. |
| `skills` | A local plugin in `plugins`, with each skill in `skills` as `<agent>:<skill>` |
| `permissions.ask` | The tool is denied: left out of `tools`, and in `disallowedTools`. Nobody can approve during an unattended run. |
| `limits.maxTurns` | `maxTurns` |
| `limits.timeout` | The launcher aborts the run |
| `output.schema` | `outputFormat` (`output_format` in Python) of type `json_schema` |
| `secretRef` | A reference in `options.json`; the launcher reads the value from the secret source when the run starts |
| `telemetry` | Claude Code's OpenTelemetry variables: metrics and logs over OTLP, with the agent's identity and `resourceAttributes` in `OTEL_RESOURCE_ATTRIBUTES` |

Every run also gets the agent's identity in `SOLARIA_LOGICAL_ID`, `SOLARIA_ENVIRONMENT`, `SOLARIA_SPEC_DIGEST` and `SOLARIA_RESOLVED_DIGEST` ([Identity](../concepts.md#identity)).

Every run uses permission mode `dontAsk`: the tools above are approved in advance, and any other call is denied instead of waiting for an answer.

Models from providers other than `anthropic` fail at plan time; fallback models from other providers are skipped with a warning.

## Sandbox

The `process` placement runs on the machine as it is, so on its own it can't enforce the sandbox:

- `packages` aren't installed: the run uses the tools the machine has. Plan warns.
- A limited `network` isn't enforced, unless `isolation` is `srt`. Plan warns unless the mode is `unrestricted`.
- `resources` aren't enforced. Plan warns.
- `image` fails plan.

### Isolating `process` runs with srt

An environment can opt in to Anthropic's [sandbox-runtime](https://github.com/anthropics/sandbox-runtime) (`srt`), which wraps the whole launcher: the SDK, its tools and stdio MCP servers share one boundary.

```yaml
kind: Environment
spec:
  provider: claude-agent-sdk
  placement: process
  sandbox:
    isolation: srt
    network: {allow: [tracker.example.com]}
```

Apply writes `srt-settings.json` into the bundle, and the recorded command becomes `srt --settings <bundle>/srt-settings.json -- node <bundle>/launch.mjs` (or the same around `launch.py`):

- **Writes** go only to srt's temp folder, which holds the run's scratch `HOME` and temp folders (Claude Code's own temp folder, `CLAUDE_CODE_TMPDIR`, moves there too), the provider's package cache in `.solaria/claude-agent-sdk/.cache`, and the workspace. The bundle, the rest of your home folder and everything else are read-only.
- **The network** is limited to the hosts the agent needs: Anthropic's API, its remote MCP servers, the registries its stdio MCP packages download from, the telemetry endpoint, `sandbox.network.allow`, and the package managers' registries with `packageManagers: true`. A telemetry endpoint on `localhost` stays reachable. `mode: unrestricted` fails plan, since srt has no way to allow every host.
- The SDK's own `sandbox` option stays off, since sandboxes don't nest.

**Needs:** `srt` on `PATH` (`npm install -g @anthropic-ai/sandbox-runtime`), installed outside any folder a run may write to, and `ripgrep` on macOS, or `bubblewrap`, `socat` and `ripgrep` on Linux. Plan fails when one is missing, and on Windows. On Linux, srt gives the run its own network namespace, so a telemetry collector on `localhost` may be unreachable; plan warns. srt is a beta research preview, which is why isolation is opt-in.

The `docker` placement enforces more:

- `packages` install in exec form: package names are arguments, never shell text, and a name starting with `-` fails plan.
- `resources.memory` becomes `docker run --memory`, 8 GiB by default.
- Docker can't enforce `network` host lists or `resources.disk`, so plan warns about them.
- `image` takes an image reference, or a Dockerfile path relative to the environment's file, which is built first.

## Settings

| Setting | Default | |
|---|---|---|
| `providers.claude-agent-sdk.runtime` | `node` | The launcher's language: `node` (`@anthropic-ai/claude-agent-sdk`) or `python` (`claude-agent-sdk`) |
| `providers.claude-agent-sdk.version` | `0.3.319` for Node, `0.2.178` for Python | The SDK version to install. The default is the version Solaria was tested with. |
| `providers.claude-agent-sdk.platform` | `linux/amd64` | `docker` only: the image platform. `linux/arm64` builds natively on Arm machines. |

The binding records the runtime and SDK version (outputs `runtime` and `sdkVersion`), since a model's results depend on the harness it runs in. Changing either plans an update although the agent's digests stay the same.

## Drift and destroy

On `process`, the host ID is the agent's folder under `.solaria/claude-agent-sdk/` and the version is the bundle's ID. Refresh hashes the bundle's files as they are on disk, so a file edited outside Solaria shows as drift, and a bundle without its SDK shows as gone. Destroy removes the agent's bundles.

On `docker`, the host ID is the image repository and the version is the image ID, so an image rebuilt or retagged outside Solaria shows as drift. Destroy removes the images and the build context.

## Limits

- Only Anthropic models run.
- MCP packages other than npm and PyPI can't run.
- `allowTools` can't hide a server's other tools from the agent, only deny them.
- The SDK's own dependencies follow its version ranges: `package-lock.json` and `requirements.lock` record the versions an apply installed, but a new apply can resolve newer ones.
- A telemetry header that comes from a secret can't reach Claude Code's exporter yet, so it's left out with a warning.
- On `docker`, the image runs as root, and a telemetry endpoint on `localhost` points at the container; use `host.docker.internal`. Plan warns.
