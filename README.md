# Solaria

**Plan and apply agent definitions across hosts.**

Solaria is a control plane for AI agent definitions. You declare an agent once: its instructions, model, tools, skills and sandbox. Solaria then plans and applies it to any host a provider supports. It records what it deployed where, so an agent keeps one identity whether it runs in a container on your laptop or on a managed agent platform.

> **Status: pre-alpha, design stage.** The architecture is recorded in [`docs/adr/`](docs/adr/). Only `solaria version` works today. See the [roadmap](#roadmap).

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

## What a card will look like

This is the planned `v1alpha1` format. It isn't implemented yet.

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
$ solaria plan -e local-gitagent
$ solaria apply -e ma-cloud
```

The same card, applied to two hosts, changes only the environment.

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
