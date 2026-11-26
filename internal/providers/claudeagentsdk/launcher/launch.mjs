// Solaria launcher for the claude-agent-sdk provider (ADR 0014). Generated
// by Solaria; don't edit. Run it from the workspace the agent works on:
//
//   node launch.mjs -p "<task>"
//   echo "<task>" | node launch.mjs
//
// It runs its Environment's setup steps in the workspace, enters srt when
// the environment isolates runs with it, isolates the run (isolate.mjs),
// calls the Claude Agent SDK with the options in options.json, and prints
// one JSON object: the SDK's result plus the agent's logical ID, resolved
// digest and SDK version. It exits 0 when the run succeeded, 1 when the run
// ended in an error, and 2 when it couldn't start.
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { SetupError, adopt, isolate, setup } from "./isolate.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const readJSON = (name) => JSON.parse(fs.readFileSync(path.join(here, name), "utf8"));

// The SDK's result fields a caller gets, by their names in the SDK.
const resultFields = [
  "subtype",
  "is_error",
  "result",
  "structured_output",
  "usage",
  "total_cost_usd",
  "num_turns",
  "session_id",
  "stop_reason",
  "errors",
  "permission_denials",
];

class LaunchError extends Error {}

// insideSRT marks the launcher srt starts, after setup.
const insideSRT = "SOLARIA_INSIDE_SRT";

// checkArgs fails early on a command line the run can't take.
function checkArgs(args) {
  if (args.length > 0) {
    if ((args[0] !== "-p" && args[0] !== "--prompt") || args.length !== 2) {
      throw new LaunchError('usage: node launch.mjs [-p "<task>"]; without -p the task is read from stdin');
    }
  } else if (process.stdin.isTTY) {
    throw new LaunchError('no task: pass -p "<task>" or pipe it on stdin');
  }
}

async function readTask(args) {
  checkArgs(args);
  if (args.length > 0) return args[1];
  const chunks = [];
  for await (const c of process.stdin) chunks.push(c);
  const task = Buffer.concat(chunks).toString("utf8");
  if (task.trim() === "") throw new LaunchError("no task: stdin was empty");
  return task;
}

// options builds the SDK's options from options.json: absolute paths, the
// system prompt's text, the run's environment, and secrets in place of
// their references.
function options(bundle, env) {
  const o = structuredClone(bundle.options);
  for (const s of Object.values(o.mcpServers)) {
    for (const key of ["env", "headers"]) {
      for (const [k, v] of Object.entries(s[key] ?? {})) {
        if (v !== null && typeof v === "object") s[key][k] = env[v.secretRef];
      }
    }
  }
  if (bundle.systemPromptFile) {
    o.systemPrompt = fs.readFileSync(path.join(here, bundle.systemPromptFile), "utf8");
  }
  if (o.plugins) o.plugins = o.plugins.map((p) => ({ ...p, path: path.join(here, p.path) }));
  o.env = env;
  o.cwd = workspace(bundle);
  return o;
}

// workspace is the environment's bound workspace, or the folder the run
// starts in.
function workspace(bundle) {
  return bundle.workspace ?? process.cwd();
}

// enterSRT runs this launcher again inside srt, with the same arguments,
// stdin and stdout, and returns its exit code. Setup is done by then, so
// srt's settings can keep a workspace it prepared from being written.
function enterSRT(bundle) {
  const argv = ["--settings", path.join(here, bundle.srt), "--", process.execPath, fileURLToPath(import.meta.url), ...process.argv.slice(2)];
  const res = spawnSync("srt", argv, { stdio: "inherit", env: { ...process.env, [insideSRT]: "1" } });
  if (res.error) throw new LaunchError(`can't start srt: ${res.error.message}`);
  return res.status ?? 1;
}

// TokenCount adds up a run's tokens, as limits.maxTokens counts them:
// input, output and cache tokens of every model call, the subagents'
// included. The SDK streams one assistant message per content block, and
// the blocks of one call share its message ID and carry its usage so far,
// so each call counts once, at its latest usage.
class TokenCount {
  #calls = new Map();
  add(m) {
    const u = m.message?.usage;
    if (u) this.#calls.set(m.message.id ?? m.uuid, u);
    return this.total();
  }
  total() {
    let n = 0;
    for (const u of this.#calls.values()) {
      n += (u.input_tokens ?? 0) + (u.output_tokens ?? 0) + (u.cache_creation_input_tokens ?? 0) + (u.cache_read_input_tokens ?? 0);
    }
    return n;
  }
}

async function main() {
  const bundle = readJSON("options.json");
  const print = (fields) => {
    const out = { ...bundle.agent, runtime: bundle.sdk.runtime, sdkVersion: bundle.sdk.version, ...fields };
    process.stdout.write(JSON.stringify(out) + "\n");
  };
  let cleanup = () => {};
  try {
    if (process.env[insideSRT] !== "1") {
      checkArgs(process.argv.slice(2));
      try {
        setup(readJSON("isolation.json"), workspace(bundle));
      } catch (err) {
        if (err instanceof SetupError) throw err;
        throw new LaunchError(err.message);
      }
      if (bundle.srt) return enterSRT(bundle);
    }
    const task = await readTask(process.argv.slice(2));
    let run;
    try {
      run = isolate(readJSON("isolation.json"), { cacheDir: path.resolve(here, bundle.cacheDir) });
    } catch (err) {
      throw new LaunchError(err.message);
    }
    cleanup = run.cleanup;
    if (!run.env.ANTHROPIC_API_KEY) throw new LaunchError("ANTHROPIC_API_KEY isn't set; the run needs an Anthropic API key");
    adopt(run.env);
    const opts = options(bundle, run.env);

    let query;
    try {
      ({ query } = await import(bundle.sdk.package));
    } catch (err) {
      throw new LaunchError(`can't load ${bundle.sdk.package}; run solaria apply to install it: ${err.message}`);
    }
    const abort = new AbortController();
    let timedOut = false;
    const timer = bundle.timeoutMs
      ? setTimeout(() => {
          timedOut = true;
          abort.abort();
        }, bundle.timeoutMs)
      : null;
    const tokens = new TokenCount();
    let overTokens = false;
    let result = null;
    try {
      for await (const m of query({ prompt: task, options: { ...opts, abortController: abort } })) {
        if (m.type === "result") result = m;
        if (bundle.maxTokens && m.type === "assistant" && tokens.add(m) > bundle.maxTokens) {
          overTokens = true;
          abort.abort();
          break;
        }
      }
    } catch (err) {
      if (!timedOut && !overTokens) throw err;
    } finally {
      clearTimeout(timer);
    }
    if (timedOut) {
      print({ subtype: "error_timeout", is_error: true, errors: [`the run took longer than limits.timeout (${bundle.timeout})`] });
      return 1;
    }
    if (overTokens) {
      print({
        subtype: "error_max_tokens",
        is_error: true,
        errors: [`the run used ${tokens.total()} tokens, more than limits.maxTokens (${bundle.maxTokens})`],
      });
      return 1;
    }
    if (result === null) throw new Error("the SDK ended without a result");
    const fields = {};
    for (const k of resultFields) if (result[k] !== undefined) fields[k] = result[k];
    print(fields);
    return result.subtype === "success" && !result.is_error ? 0 : 1;
  } catch (err) {
    if (err instanceof SetupError) {
      print({ subtype: "error_setup", is_error: true, errors: [err.message] });
      return 2;
    }
    const launch = err instanceof LaunchError;
    print({ subtype: launch ? "error_launch" : "error_during_execution", is_error: true, errors: [err.message] });
    return launch ? 2 : 1;
  } finally {
    cleanup();
  }
}

process.exitCode = await main();
