---
status: proposed
date: 2026-11-10
decision-makers: Renan Tarouco da Fonseca
---

# Run inputs: agents declare a workspace and repositories, environments bind them

## Context and problem statement

Most agents work on files: a product repository they read, a branch they write. Today the workspace is implicit: on `process` it's the folder the run starts in, on `docker` it's that folder mounted read-write, and on Managed Agents it's whatever the caller attaches to the session. Nothing says whether an agent may write to it, and an agent can't say it needs a repository at all. Managed Agents can attach GitHub repositories natively; other git hosts, and self-hosted workers, get nothing. Solaria never starts a run (ADR 0002), so these are inputs the caller provides, but the definition should say what an agent needs and with which access, and every host should enforce that access or refuse. How are run inputs declared, bound and enforced?

## Decision drivers

- What an agent may touch is part of what the agent is, so it belongs in the digest and in review.
- Where an input comes from (a path on a laptop, a git URL, a credential) differs per environment.
- Use a host's native mechanism when it has one; otherwise Solaria's launcher does the work.
- Fail closed: an access limit a placement can't enforce fails plan instead of being ignored.
- Solaria stays a control plane: it describes inputs and records how to provide them; it doesn't run anything.

## Considered options

- The Agent declares inputs, the Environment binds their sources, launchers or hosts enforce them.
- The Environment alone configures a workspace mount and repositories to clone; agents don't know.
- Only a read-only flag for the workspace; repositories stay the caller's problem.

## Decision outcome

Chosen option: "The Agent declares inputs, the Environment binds their sources", because access is part of the agent's identity while locations are part of the environment, the same split as tools and secrets.

**Declaring.** The Agent says what it needs and with which access:

```yaml
kind: Agent
spec:
  workspace: {access: read-only}          # or read-write; default read-write
  repositories:
    - name: product
      access: read-only
```

Both are part of the definition, so they're in `specDigest` and `resolvedDigest`. The workspace enters the digest input only when it's read-only, so agents that keep the default keep their digests (ADR 0008). An environment's override can make the workspace read-only, never writable again, like the other narrowing overrides.

**Binding.** The Environment says where each one comes from:

```yaml
kind: Environment
spec:
  inputs:
    workspace: {path: .}                  # relative to this file; default: the folder the run starts in
    repositories:
      product:
        url: https://dev.example.com/org/product.git
        ref: main
        credential: {vault: git, name: git-host}   # a Vault credential (ADR 0016)
```

`validate` fails when an agent declares a repository the environment doesn't bind. Bindings aren't in any digest: they describe the environment, like the sandbox.

**Native where possible, otherwise the launcher.**

| Placement | Workspace | Repositories |
|---|---|---|
| `process` | The working directory; read-only only with srt isolation (below) | The launcher clones each one before the run |
| `docker` | Mounted `:ro` or read-write | The launcher clones each one inside the container |
| `managed-agents` / `self-hosted` | The session's; read-only, or a bound folder, fails plan | The worker container clones each one |
| `managed-agents` / `cloud` | What the caller attaches; read-only, or a bound folder, fails plan | GitHub: a `github_repository` resource listed in the invocation for the caller to attach. Any other git host fails plan, naming the repository: no Solaria code runs there |

A cloned repository lands at `repositories/<name>` inside the workspace. A read-only repository is cloned, then made unwritable the same way as a read-only workspace. Credentials reach the clone through the Vault credential and never appear in a URL, a log or state.

The invocation (ADR 0014) records each input: the workspace access (`workspaceAccess`), a bound folder (`workspace: path` and `workspacePath`), and each repository with its name, URL, ref and access, and, where the caller must attach it, how. `status -a` shows them.

**Sandboxing the `process` placement.** A `process` run has no isolation of its own, so a read-only workspace can't be enforced there by default. An environment can opt in to Anthropic's sandbox-runtime (`srt`), which wraps the whole launcher, so the SDK, its tools and stdio MCP servers share one boundary:

```yaml
kind: Environment
spec:
  placement: process
  sandbox:
    isolation: srt
```

- The launcher command becomes `srt --settings <bundle>/srt-settings.json -- node launch.mjs`, or the same around `launch.py`. Apply writes the settings into the bundle, so the bundle's digest covers them and editing them shows as drift:
  - writes are allowed only to srt's temp folder, which holds the run's scratch HOME and temp folder, the provider's package cache, and the workspace and repositories the agent declares writable;
  - the domain allow-list is the hosts the agent needs (the model API, its remote MCP servers, the registries its stdio MCP packages come from, the telemetry endpoint) plus `sandbox.network`, so `process` enforces the network too. A loopback endpoint, such as a local telemetry collector, is reached through srt's local binding rather than the list.
- srt has no way to allow every host, so `isolation: srt` with `network.mode: unrestricted` fails plan.
- srt is installed outside every writable path. The SDK's own `sandbox` option stays off, since sandboxes don't nest. The launcher's allowlist passes srt's proxy variables through, and tells Node's own `fetch` to use them.
- It works on macOS and on Linux with bubblewrap and socat. On Windows, and wherever srt is missing, plan fails when `isolation: srt` is set.
- It's opt-in because srt is a beta research preview whose configuration may change. Without it, `process` behaves as ADR 0014 describes, and plan warns as before.
- A read-only workspace also adds deny rules for the file-editing tools, as defense in depth.

**Failing closed.** A read-only workspace or repository on a placement that can't enforce it, such as `process` without isolation, fails plan, naming the agent and offering `docker` or `sandbox.isolation: srt`. This differs from budgets and the sandbox's resource fields, which warn: access limits are a promise about what an agent can do, so they're never silently dropped.

### Consequences

- Good, because what an agent may touch is visible in its card and in review, and a placement that can't keep that promise refuses at plan time.
- Good, because repositories work on any git host wherever Solaria's code runs, and natively on GitHub with Managed Agents.
- Good, because opting into srt also gives `process` real network limits.
- Bad, because a non-GitHub repository can't be used on Managed Agents cloud until the host supports it.
- Bad, because srt adds an external dependency, in beta, for isolated `process` runs.
- Bad, because the default access is read-write, matching today's behavior, so existing cards keep working but aren't least-privilege until someone sets `read-only`.

### Confirmation

- Validation tests cover an unbound repository and a credential that isn't in a referenced vault.
- Golden tests show the srt settings, the `:ro` mount and the clone steps per placement.
- Plan tests cover each fail-closed case: read-only on plain `process`, `isolation: srt` on Windows, and a non-GitHub repository on Managed Agents cloud.
- An opt-in integration test runs a read-only workspace under srt and checks that a write fails.

## Pros and cons of the options

### The Environment alone configures inputs

- Good, because cards stay unchanged and nothing enters the digest.
- Bad, because an agent's access would differ per environment without any trace in its definition, and review couldn't see it.

### Only a read-only flag

- Good, because it's the smallest change.
- Bad, because repositories, the most common input, would stay the caller's problem on every host.

## More information

- [ADR 0002](0002-control-plane-not-agent-framework.md): Solaria doesn't start runs.
- [ADR 0007](0007-portable-sandbox-is-packages.md): the portable sandbox.
- [ADR 0014](0014-process-placement-and-claude-agent-sdk.md): the `process` placement and the invocation.
- [ADR 0016](0016-vault-scoped-credentials.md): Vault credentials.
- [Claude Code sandboxing](https://code.claude.com/docs/en/sandboxing) and [sandbox-runtime](https://github.com/anthropics/sandbox-runtime). The SDK's own `sandbox` option covers only Bash and its children, which is why the whole launcher is wrapped instead. Seatbelt (macOS) and Landlock (Linux) were also considered; neither can filter by domain without a proxy.
