# Reviewer: one agent, three hosts

A code review agent, deployed unchanged to three environments:

| Environment | Where the agent loop runs | Where its tools run | Needs |
|---|---|---|---|
| `local-gitagent` | gitagent, in a container here | the same container | Docker |
| `ma-cloud` | Claude Managed Agents | Anthropic's cloud sandbox | `ANTHROPIC_API_KEY` |
| `ma-self-hosted` | Claude Managed Agents | a worker container here | `ANTHROPIC_API_KEY`, Docker, an environment key |

[`agent.yaml`](agent.yaml) holds the agent and the MCP server it uses. [`environments.yaml`](environments.yaml) holds the three environments. Moving the agent between hosts means choosing another environment, not editing the card.

## Plan

Planning needs no credentials, Docker or network, because there's no state yet:

```console
$ solaria validate
valid: 1 Agent, 3 Environments, 1 McpServer
$ solaria plan -e local-gitagent
$ solaria plan -e ma-cloud
$ solaria plan -e ma-self-hosted
```

`ma-cloud` and `ma-self-hosted` show the same `resolvedDigest`: the same definition runs in both. `local-gitagent` overrides the model with a cheaper one for local runs, so its `resolvedDigest` differs. Its `specDigest`, the card as written, is the same everywhere; `solaria status` shows it after apply.

The warnings are each host saying what it can't do: Managed Agents doesn't enforce `limits`, and Docker doesn't enforce network host lists.

## Apply

**`local-gitagent`** builds an image. The first build takes several minutes. `apply` prints the `docker run` command, and `solaria status -e local-gitagent -a reviewer` shows it again later with the variables it needs. The task goes after it:

```console
$ solaria apply -e local-gitagent
$ export ANTHROPIC_API_KEY=...
$ docker run ... solaria/local-gitagent/reviewer:<digest> -p "Review the last commit."
```

**`ma-cloud`** creates real objects in the key's Anthropic organization: an environment named `reviewer-example-ma-cloud`, the `review-rubric` skill, and an agent named `reviewer-example-reviewer`. Start sessions from the Claude Console or the API with the agent and environment IDs that `solaria status` shows.

```console
$ export ANTHROPIC_API_KEY=...
$ solaria apply -e ma-cloud
$ solaria status
```

**`ma-self-hosted`** needs two applies. The first creates the environment and stops, explaining how to get its key from the Claude Console. Store the key as `ANTHROPIC_ENVIRONMENT_KEY`; the second apply builds the worker image and starts the `solaria-ma-self-hosted-worker` container.

## Change and destroy

Edit [`instructions/rules.md`](instructions/rules.md) and plan again: the agent shows `~` with a new `resolvedDigest`. On Managed Agents, apply creates a new agent version.

```console
$ solaria destroy -e local-gitagent
$ solaria destroy -e ma-cloud
```

On Managed Agents, destroy **archives** the agent, which can't be undone, and deletes its skills and environment.
