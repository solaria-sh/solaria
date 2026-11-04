---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# Record architecture decisions

## Context and problem statement

Solaria's design was worked out in conversation before any code existed. Contributors, and the coding agents that help build it, need to know what was decided, why, and which alternatives were rejected, without having to reconstruct it from commits.

## Decision outcome

Chosen option: "Architecture decision records in `docs/adr/`, using MADR", because MADR is a widely used, lightweight Markdown format that lives next to the code and is reviewed in pull requests.

### Consequences

- Good, because decisions are reviewable, linkable and versioned with the code they shape.
- Good, because agents working in the repo can be pointed at them (see `AGENTS.md`).
- Bad, because ADRs have to be kept current: a pull request that changes a decision must update or supersede its ADR.

### Confirmation

Review checks that structural changes come with an ADR. Superseded ADRs keep their file and change their status to `superseded by NNNN`.

## More information

- [MADR](https://adr.github.io/madr/)
- [Michael Nygard, "Documenting Architecture Decisions"](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions)
