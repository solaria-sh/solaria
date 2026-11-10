# Getting started

In about ten minutes you'll define an agent, apply it to an environment, change it, see the plan for that change, and remove it again. Everything runs on your machine with the `stub` provider, which records what it deploys in files, so you need no API key, no Docker and no account.

You'll go through the same steps you later use on a real host. Only the environment changes.

## Before you begin

You need a terminal on macOS, Linux or Windows. Nothing else: Solaria is one binary.

## Install Solaria

Pick the line for your system:

```bash
# macOS and Linux, with Homebrew
brew install solaria-sh/tap/solaria

# macOS and Linux, without Homebrew
curl -fsSL https://solaria.sh/install.sh | sh

# Windows, in PowerShell
irm https://solaria.sh/install.ps1 | iex
```

Check that it's on your `PATH`:

```console
$ solaria version
```

It prints the version, the commit it was built from, and your platform.

## Define an agent

Make a folder for the project. Solaria reads every `.yaml` file in it.

```console
$ mkdir hello && cd hello
```

An agent's instructions are Markdown files. Create `instructions.md`:

```markdown
Greet the user by name, in one short sentence.
```

Then create `solaria.yaml` with an [Agent](resources/agent.md). It says what the agent is (its instructions, model, tools and limits) and names no host:

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Agent
metadata:
  name: hello
spec:
  description: Says hello
  instructions: [instructions.md]
  model:
    preferred: anthropic:claude-haiku-5-5
  tools:
    builtin: [read]
  limits: {maxTurns: 5}
```

## Add an environment

An [Environment](resources/environment.md) is the place agents run: it names a provider and a placement. Add this to the end of `solaria.yaml`, after a `---` separator:

```yaml
---
apiVersion: solaria.sh/v1alpha1
kind: Environment
metadata:
  name: local
spec:
  provider: stub
  placement: file
```

Check both resources:

```console
$ solaria validate
valid: 1 Agent, 1 Environment
```

If you mistype a field, `validate` names the file, line and column, and suggests the field you meant.

## Plan

`plan` shows what `apply` would do, and changes nothing:

```console
$ solaria plan -e local
Environment local (stub/file)

  + Agent/hello
      resolvedDigest: sha256:08ffaed7b158

Plan: 1 to create, 0 to update, 0 to replace, 0 to delete.
```

`+` means the agent will be created. The `resolvedDigest` is the agent's identity, a hash of its definition and its instruction files. You'll see the same `08ffaed7b158`: the same definition has the same digest on every machine and every host.

## Apply

`apply` shows the plan again and asks before it changes anything. Type `yes`:

```console
$ solaria apply -e local
Environment local (stub/file)

  + Agent/hello
      resolvedDigest: sha256:08ffaed7b158

Plan: 1 to create, 0 to update, 0 to replace, 0 to delete.

Apply these changes to local? Only 'yes' is accepted: yes

Apply complete: 1 created, 0 updated, 0 replaced, 0 deleted. State serial 1.
  Agent/hello: stub_f8b128522370 version 1 (sha256:08ffaed7b158)
```

The stub provider wrote the agent to `.solaria/stub/local/hello.json`, where a real provider would have built an image or called an API. Solaria recorded what it deployed in `.solaria/state/local.json`. `status` reads that state:

```console
$ solaria status
Environment local (state serial 1)
  OBJECT       PROVIDER   HOST ID            VERSION  DIGEST               APPLIED           CONFIGURATION
  Agent/hello  stub/file  stub_f8b128522370  1        sha256:08ffaed7b158  2026-11-08 10:40  up to date
```

Plan again and there's nothing to do:

```console
$ solaria plan -e local
Environment local (stub/file)

No changes. The environment matches the configuration.
```

## Change the agent

Edit `instructions.md` so the agent answers in two sentences:

```markdown
Greet the user by name, in two short sentences.
```

`status` now says the configuration `changed`, and `plan` shows what changed, field by field:

```console
$ solaria plan -e local
Environment local (stub/file)

  ~ Agent/hello
      ~ instructions[0]: text changed (1 line -> 1 line)
      resolvedDigest: sha256:08ffaed7b158 -> sha256:e1d64fc6f919

Plan: 0 to create, 1 to update, 0 to replace, 0 to delete.
```

`~` is an update. The digest moved because the instructions are part of the agent's identity. Apply it:

```console
$ solaria apply -e local
...
Apply complete: 0 created, 1 updated, 0 replaced, 0 deleted. State serial 2.
  Agent/hello: stub_f8b128522370 version 2 (sha256:e1d64fc6f919)
```

The host ID stayed the same and the version went to 2: it's the same agent, in a new version.

## Clean up

`destroy` removes everything Solaria deployed to the environment. It asks first, like `apply`:

```console
$ solaria destroy -e local
Environment local (stub/file)

  - Agent/hello
      host: stub_f8b128522370 version 2

Plan: 0 to create, 0 to update, 0 to replace, 1 to delete.

Destroy everything Solaria deployed to local? Only 'yes' is accepted: yes

Destroy complete: 0 created, 0 updated, 0 replaced, 1 deleted. State serial 3.
```

## What you did

You declared an agent once, without naming a host. You put it in an environment, planned and applied it, changed it and saw the change as a field diff and a new digest, then destroyed it. That's the whole workflow, and it doesn't change from one host to the next.

## Next steps

- **Run it on a real host.** Add a second environment to `solaria.yaml` and apply the same agent there. [`gitagent`](providers/gitagent.md) builds it into a container image with Docker. [`claude-agent-sdk`](providers/claude-agent-sdk.md) runs it on the Claude Agent SDK, as a local Node or Python process or in a container. [`managed-agents`](providers/managed-agents.md) deploys it to Claude Managed Agents with an `ANTHROPIC_API_KEY`. The agent and its digest stay the same.
- **Understand the model.** [Concepts](concepts.md) covers environments, providers, identity, state and drift.
- **Look up a field.** The resource pages list every field of [Agent](resources/agent.md), [Environment](resources/environment.md) and [McpServer](resources/mcp-server.md).
- **Get completion in your editor.** `solaria schema > solaria.schema.json` saves the JSON Schema; point the YAML language server at it with `# yaml-language-server: $schema=./solaria.schema.json` at the top of each file.
- **See a larger project.** [`examples/reviewer`](../examples/reviewer) takes one agent with an MCP server and a skill through three hosts.
