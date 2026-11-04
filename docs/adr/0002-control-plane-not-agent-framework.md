---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# A control plane, not an agent framework

## Context and problem statement

Every agent host defines agents in its own format. Claude Managed Agents takes a versioned agent definition. Microsoft Foundry hosted agents take a container image. Runtimes like gitagent and Docker's cagent read their own YAML folders. Moving an agent between hosts means rewriting it.

As of November 2026, nothing defines an agent once and deploys it across hosts:

- Oracle's Open Agent Specification is portable across **frameworks** (LangGraph, AutoGen, CrewAI), not hosts.
- gitagent and cagent are runtimes.
- Terraform providers exist for single hosts (Foundry through `azapi`, a community Managed Agents provider), each with its own resource schema.

What should Solaria be?

## Decision drivers

- Portability across hosts, not frameworks.
- A scope small enough for v0 to ship.
- Don't compete with runtimes that already work well.

## Considered options

- An agent framework with its own loop, deployable anywhere.
- An adapter layer that converts between existing formats.
- A control plane: one definition, plan/apply per host, and state that records what was deployed where.

## Decision outcome

Chosen option: "A control plane", because it follows the pattern infrastructure-as-code tools proved: shared workflow, state and language, with a provider per host. And it doesn't require building another agent runtime.

Solaria never runs an agent loop. Execution belongs to a runtime (gitagent, the Claude Agent SDK, …) or a host (Managed Agents, …), and each one is a provider target.

v0 has no `run` command either. Terraform doesn't run what it deploys. Whether starting a session belongs in Solaria will be reassessed once v0 is in use.

v0 commands: `validate`, `plan`, `apply`, `status`, `destroy`, plus `schema` and `version`.

### Consequences

- Good, because the scope is bounded: definitions, diffing, deployment and state.
- Good, because any runtime can become a target without Solaria reimplementing it.
- Bad, because behavior still differs between runtimes running the same definition. Solaria makes deployment portable, not behavior identical. The CAIS 2026 Agent Spec study measured exactly this kind of runtime variance.
- Bad, because users still need a separate way to start sessions in v0.

### Confirmation

No package in the repository implements a model-call loop. Proposals for one need an ADR that supersedes this one.

## More information

- [Open Agent Specification](https://arxiv.org/abs/2510.04173) and the [CAIS 2026 cross-framework comparison](https://www.caisconf.org/program/2026/papers/open-agent-specification-a-unified-representation-for-ai-agents/)
- [gitagent](https://github.com/open-gitagent/gitagent), [Docker cagent](https://docker.github.io/cagent/getting-started/introduction)
- [Deploying a Foundry hosted agent with Terraform](https://learn.microsoft.com/en-us/azure/foundry/agents/how-to/deploy-hosted-agent-terraform)
- [Declarative agent specification survey (Zylos, 2026)](https://zylos.ai/research/2026-04-22-declarative-agent-specification-configuration-languages/)
