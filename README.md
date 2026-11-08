# Solaria

**Plan and apply agent definitions across hosts.**

Solaria is a control plane for AI agent definitions. You declare an agent once: its instructions, model, tools, skills and sandbox. Solaria then plans and applies it to any host a provider supports. It records what it deployed where, so an agent keeps one identity whether it runs in a container on your laptop or on a managed agent platform.

> **Status: pre-alpha.** The full workflow (`validate`, `plan`, `apply`, `status`, `destroy`) works with three providers: `stub` for tests, `gitagent` in a local container, and Claude Managed Agents in the cloud or with a self-hosted sandbox. The format may still change before 1.0. Start with the [concepts](docs/concepts.md), and see the [roadmap](#roadmap).

## Why

Every agent host has its own definition format. Moving an agent from your machine to a hosted platform, or between platforms, means rewriting it. Infrastructure solved the same problem with infrastructure-as-code tools. They don't make an AWS VM and an Azure VM the same thing. What they share is the workflow, the state and the language, and each cloud gets its own provider.

Solaria applies that pattern to agents:

| Infrastructure as code | Solaria |
|---|---|
| Configuration language | Agent cards: Kubernetes-style YAML built on AGENTS.md, [Agent Skills](https://agentskills.io) and [MCP](https://modelcontextprotocol.io) |
| Providers | Host providers: `gitagent` in a local container, Claude Managed Agents, and more later |
| State | Which logical agent, with which content digest, is deployed where, under which host ID and version |
| `plan` / `apply` | Diff a card against what's deployed, then sync it |
| Workspaces | Environments (`local`, `staging`, `prod`), each able to override provider and model |

Solaria never runs the agent loop itself. Existing runtimes and platforms do that well. Solaria defines, deploys and tracks.

Some hosts have their own declarative tooling, such as `ant apply` for Claude Managed Agents. Those tools cover one host each. Solaria's job is one neutral definition that keeps the same identity across all of them.

## What a card looks like

This is the `v1alpha1` format. Every field is in the [card reference](docs/reference.md).

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Agent
metadata:
  name: reviewer
spec:
  description: Reviews pull requests and flags risky changes
  instructions: [instructions/role.md]
  model:
    preferred: anthropic:claude-sonnet-5-5
    fallback: [openai:gpt-6-astra]
  tools:
    builtin: [shell, read]
    mcp:
      - ref: git-host
        allowTools: [get_pull_request, add_comment]
  skills: [../skills/review-rubric]
```

```console
$ solaria validate
valid: 1 Agent, 2 Environments, 1 McpServer
$ solaria plan -e local-gitagent
$ solaria apply -e ma-cloud
```

The same card, applied to two hosts, changes only the environment.

## Install

Install instructions live at [solaria.sh](https://solaria.sh). In short:

```bash
# macOS and Linux, with Homebrew
brew install solaria-sh/tap/solaria

# macOS and Linux, without Homebrew
curl -fsSL https://solaria.sh/install.sh | sh

# Windows, in PowerShell
irm https://solaria.sh/install.ps1 | iex

# From source, with Go
go install github.com/solaria-sh/solaria/cmd/solaria@latest
```

Release archives and `checksums.txt` are on the [releases page](https://github.com/solaria-sh/solaria/releases).

## Try it

[`examples/hello`](examples/hello) runs the whole workflow with the `stub` provider. It writes its "host objects" to files, so it needs no credentials:

```console
$ go install github.com/solaria-sh/solaria/cmd/solaria@latest
$ cd examples/hello
$ solaria plan -e local
Environment local (stub/file)

  + Agent/hello
      resolvedDigest: sha256:08ffaed7b158

Plan: 1 to create, 0 to update, 0 to replace, 0 to delete.
$ solaria apply -e local
$ solaria status
$ solaria destroy -e local
```

The same agent, in its `local-gitagent` environment, becomes a [gitagent](https://github.com/open-gitagent/gitagent) container image. Its digest is the same, because only the environment differs. This needs Docker, and the first build takes several minutes. `apply` prints the command that runs the image, and the task goes after it:

```console
$ solaria apply -e local-gitagent
$ export ANTHROPIC_API_KEY=...
$ docker run --rm --platform linux/amd64 --memory 8g --volume "$PWD":/workspace \
    --env ANTHROPIC_API_KEY solaria/local-gitagent/hello:08ffaed7b158 -p "Say hello."
```

In `ma-cloud`, the same agent goes to [Claude Managed Agents](https://platform.claude.com/docs/en/managed-agents/overview), which runs it on Anthropic's side. Again only the environment differs. `apply` creates real objects in your Anthropic organization, named `hello-example-*`, and `destroy` archives the agent, which can't be undone:

```console
$ export ANTHROPIC_API_KEY=...
$ solaria plan -e ma-cloud
Environment ma-cloud (managed-agents/cloud)

  + Environment/ma-cloud

  + Agent/hello
      resolvedDigest: sha256:08ffaed7b158

Plan: 2 to create, 0 to update, 0 to replace, 0 to delete or archive.
$ solaria apply -e ma-cloud
```

`ma-self-hosted` keeps the agent loop on Anthropic's side but runs its tools in a worker container on your machine. Its first `apply` creates the environment and stops, saying how to get the environment's key from the Claude Console. Store the key as `ANTHROPIC_ENVIRONMENT_KEY` and apply again to start the worker.

[`examples/reviewer`](examples/reviewer) takes one agent through all three hosts.

For completion and checks in your editor, save the schema and point the YAML language server at it:

```console
$ solaria schema > solaria.schema.json
```

```yaml
# yaml-language-server: $schema=./solaria.schema.json
apiVersion: solaria.sh/v1alpha1
```

## Documentation

The [documentation](docs/README.md) covers the concepts, every card field, each provider, and the decisions behind them.

## Roadmap

| Milestone | Scope |
|---|---|
| M0 | Repository foundation and architecture decision records |
| M1 | Spikes: gitagent v3 and Claude Managed Agents API details |
| M2 | Core model, JSON Schema, `solaria validate` |
| M3 | Environment overlays, references, content digests |
| M4 | State, plan/apply engine, CLI |
| M5 | `gitagent` provider (Docker placement) |
| M6 | `managed-agents` provider (cloud and self-hosted placements) |
| M7 | Examples, docs, first release |

## The name

In Isaac Asimov's robot novels, Solaria is a planet where robots do nearly all of the work, and the few humans who live there set the direction. That's the idea: agents do the work, people steer.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md). To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE).

Solaria is not affiliated with HashiCorp. Terraform is a trademark of HashiCorp, Inc., and is mentioned here only to describe the pattern Solaria follows.
