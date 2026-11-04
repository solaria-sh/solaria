---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# Promotion and autonomy policy are out of scope

## Context and problem statement

Teams that run agents in production often promote them gradually. One example is an autonomy ladder that goes from shadow, to propose, to act with approval, to act and report, gated on eval scores and intervention rates. Should Solaria implement that policy?

## Decision drivers

- Keep Solaria generic.
- Policies differ widely between teams.
- Solaria should still make such policies easy to build.

## Considered options

- Built-in promotion stages and gates.
- Building blocks only: digests, history, environments and outputs.

## Decision outcome

Chosen option: "Building blocks only". Promotion means applying the same `specDigest` to the next environment, and `plan` shows that it's the definition already tested. Autonomy levels, eval gates and intervention metrics belong to the system using Solaria (for example, an agent factory and its CI), not to Solaria itself.

Building blocks Solaria provides:

- `specDigest` and `resolvedDigest` (ADR 0008);
- per-environment state with `history` (ADR 0009);
- environments, which can model stages like `shadow`;
- outputs other tools can read, such as the current host binding.

Triggers and evals are also out of v0's scope.

### Consequences

- Good, because Solaria stays small and works with any promotion policy.
- Bad, because every team has to build its own policy on top.

### Confirmation

Feature requests for promotion stages are answered by exposing more data, not more policy, unless an ADR supersedes this one.
