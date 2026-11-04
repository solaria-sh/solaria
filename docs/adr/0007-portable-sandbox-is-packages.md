---
status: accepted
date: 2026-11-04
decision-makers: Renan Tarouco da Fonseca
---

# The portable sandbox is a package list

## Context and problem statement

An Environment describes the sandbox agents run in. Container placements can build any image, but Managed Agents cloud environments accept no custom image: only `packages` (apt, cargo, gem, go, npm, pip) and `networking`. If the sandbox were described as a Dockerfile, no environment could move to the cloud.

## Decision drivers

- A sandbox description that works on every placement.
- Local containers that behave like the cloud sandbox.
- An escape hatch for needs packages can't express.

## Considered options

- A Dockerfile or image as the sandbox definition.
- A package list as the portable form, with an image as an escape hatch.

## Decision outcome

Chosen option: "A package list as the portable form". It follows Managed Agents' own shape:

```yaml
sandbox:
  packages: { apt: [dotnet-sdk-8.0], npm: [pnpm], pip: [] }
  network:  { mode: limited, allow: [dev.azure.com], packageManagers: true }
  resources: { memory: 8Gi, disk: 10Gi }
  # image: ./custom.Dockerfile    # container placements only
```

Container placements generate the Dockerfile from the packages. It uses `FROM ubuntu:22.04` for `linux/amd64` with the same resource caps, matching the documented cloud sandbox spec, so local runs behave like the cloud. `image` is an escape hatch: container placements accept it, and `managed-agents/cloud` rejects it at plan time.

### Consequences

- Good, because one package list produces both the cloud environment and the local image.
- Good, because developers get cloud-like sandboxes without writing Dockerfiles.
- Bad, because some tools can't be installed through these package managers (for example, NuGet isn't one of them). Those environments must use `image` and give up the cloud placement.

### Confirmation

Golden tests cover Dockerfile generation, and capability tests confirm `cloud` rejects `image`.

## More information

- [Managed Agents: cloud environment setup](https://platform.claude.com/docs/en/managed-agents/environments)
- [Managed Agents: cloud sandbox reference](https://platform.claude.com/docs/en/managed-agents/cloud-sandboxes-reference)
