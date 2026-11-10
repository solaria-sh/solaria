# Solaria launcher for the claude-agent-sdk provider (ADR 0014). Generated
# by Solaria; don't edit. Run it with the bundle's own Python, from the
# workspace the agent works on:
#
#   venv/bin/python launch.py -p "<task>"
#   echo "<task>" | venv/bin/python launch.py
#
# It isolates the run (isolate.py), calls the Claude Agent SDK with the
# options in options.json, and prints one JSON object: the SDK's result plus
# the agent's logical ID, resolved digest and SDK version. It exits 0 when
# the run succeeded, 1 when the run ended in an error, and 2 when it
# couldn't start.
import sys

# Importing isolate.py mustn't write into the bundle.
sys.dont_write_bytecode = True

import asyncio  # noqa: E402
import copy  # noqa: E402
import json  # noqa: E402
import os  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from isolate import adopt, isolate  # noqa: E402

# The SDK's result fields a caller gets, by their names in the SDK.
RESULT_FIELDS = [
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
]

# options.json uses the TypeScript SDK's names; these are Python's.
OPTION_NAMES = {
    "model": "model",
    "fallbackModel": "fallback_model",
    "effort": "effort",
    "maxTurns": "max_turns",
    "tools": "tools",
    "allowedTools": "allowed_tools",
    "disallowedTools": "disallowed_tools",
    "permissionMode": "permission_mode",
    "settingSources": "setting_sources",
    "strictMcpConfig": "strict_mcp_config",
    "mcpServers": "mcp_servers",
    "skills": "skills",
    "plugins": "plugins",
}


class LaunchError(Exception):
    pass


def read_json(name):
    with open(os.path.join(HERE, name), encoding="utf-8") as f:
        return json.load(f)


def read_task(args):
    if args:
        if args[0] not in ("-p", "--prompt") or len(args) != 2:
            raise LaunchError('usage: python launch.py [-p "<task>"]; without -p the task is read from stdin')
        return args[1]
    if sys.stdin is None or sys.stdin.isatty():
        raise LaunchError('no task: pass -p "<task>" or pipe it on stdin')
    task = sys.stdin.read()
    if task.strip() == "":
        raise LaunchError("no task: stdin was empty")
    return task


def options(bundle, env):
    """Build the SDK's options from options.json: absolute paths, the system
    prompt's text, the run's environment, and secrets in place of their
    references."""
    o = copy.deepcopy(bundle["options"])
    for server in o["mcpServers"].values():
        for key in ("env", "headers"):
            for k, v in (server.get(key) or {}).items():
                if isinstance(v, dict):
                    server[key][k] = env[v["secretRef"]]
    if "plugins" in o:
        o["plugins"] = [dict(p, path=os.path.join(HERE, p["path"])) for p in o["plugins"]]
    out = {OPTION_NAMES[k]: v for k, v in o.items()}
    if bundle.get("systemPromptFile"):
        with open(os.path.join(HERE, bundle["systemPromptFile"]), encoding="utf-8") as f:
            out["system_prompt"] = f.read()
    out["env"] = env
    out["cwd"] = os.getcwd()
    return out


async def run_query(sdk, task, opts, timeout):
    """Return the SDK's result message, or None when there was none. Raise
    asyncio.TimeoutError when the run takes longer than timeout seconds."""
    result = None

    async def consume():
        nonlocal result
        async for m in sdk.query(prompt=task, options=sdk.ClaudeAgentOptions(**opts)):
            if isinstance(m, sdk.ResultMessage):
                result = m

    if timeout:
        await asyncio.wait_for(consume(), timeout)
    else:
        await consume()
    return result


def main():
    bundle = read_json("options.json")

    def emit(fields):
        out = dict(bundle["agent"], runtime=bundle["sdk"]["runtime"], sdkVersion=bundle["sdk"]["version"])
        out.update(fields)
        sys.stdout.write(json.dumps(out, default=str) + "\n")
        sys.stdout.flush()

    cleanup = None
    try:
        task = read_task(sys.argv[1:])
        try:
            env, _, cleanup = isolate(read_json("isolation.json"), os.path.normpath(os.path.join(HERE, bundle["cacheDir"])))
        except (RuntimeError, ValueError, OSError) as err:
            raise LaunchError(str(err)) from None
        if not env.get("ANTHROPIC_API_KEY"):
            raise LaunchError("ANTHROPIC_API_KEY isn't set; the run needs an Anthropic API key")
        adopt(env)
        opts = options(bundle, env)
        try:
            import claude_agent_sdk as sdk
        except ImportError as err:
            raise LaunchError(f"can't load {bundle['sdk']['package']}; run solaria apply to install it: {err}") from None

        timeout = bundle.get("timeoutMs", 0) / 1000
        try:
            result = asyncio.run(run_query(sdk, task, opts, timeout))
        except asyncio.TimeoutError:
            emit({
                "subtype": "error_timeout",
                "is_error": True,
                "errors": [f"the run took longer than limits.timeout ({bundle['timeout']})"],
            })
            return 1
        if result is None:
            raise RuntimeError("the SDK ended without a result")
        fields = {}
        for k in RESULT_FIELDS:
            v = getattr(result, k, None)
            if v is not None:
                fields[k] = v
        emit(fields)
        return 0 if fields.get("subtype") == "success" and not fields.get("is_error") else 1
    except LaunchError as err:
        emit({"subtype": "error_launch", "is_error": True, "errors": [str(err)]})
        return 2
    except Exception as err:  # noqa: BLE001 - every failure is reported as JSON
        emit({"subtype": "error_during_execution", "is_error": True, "errors": [str(err) or type(err).__name__]})
        return 1
    finally:
        if cleanup is not None:
            cleanup()


if __name__ == "__main__":
    sys.exit(main())
