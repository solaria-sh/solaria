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

Container placements generate the Dockerfile from the packages. They build on a **Solaria sandbox base image** that reproduces the documented cloud sandbox:

- Ubuntu 24.04 LTS on `linux/amd64`;
- its preinstalled runtimes: Python 3.12+, Node 20+, Go, Rust, Java 21+, Ruby, PHP, GCC 13+.

The card's `packages` are installed on top, with the same 8 GB / 10 GB caps. A bare `FROM ubuntu` isn't enough: the gitagent spike found that Ubuntu's own packages give Node 12 and Python 3.10 on 22.04, far behind the cloud. `image` is an escape hatch: container placements accept it, and `managed-agents/cloud` rejects it at plan time.

**Mapping to Managed Agents** (see the [Managed Agents spike](../spikes/2026-11-managed-agents-api.md)):

| Solaria | Managed Agents cloud environment |
|---|---|
| `packages.<manager>` | `packages.<manager>` (apt, cargo, gem, go, npm, pip; native pin syntax) |
| `network.mode: limited` | `networking.type: limited` |
| `network.allow` | `networking.allowed_hosts` |
| `network.packageManagers` | `networking.allow_package_managers` (required to be `true` when packages are set) |
| MCP hosts of the bound agents | `networking.allow_mcp_servers`, or added to `allowed_hosts`; the provider derives it |
| `resources` | not configurable (fixed limits); a value above them fails plan, others are ignored with a note |
| `network` omitted | sent explicitly as `limited` with no extra hosts, never left to the host's `unrestricted` default |

**As implemented in M5** (`internal/providers/gitagent`):

- **No published base image yet.** The base is a stage of the generated Dockerfile, so Docker's layer cache shares it across agents, and nothing has to be published first.
- **Pinned inputs:**
  - `ubuntu:noble-<date>`;
  - Node 24, copied from the official `node` image (Ubuntu 24.04's apt has Node 18);
  - Python 3.12, GCC 13, OpenJDK 21, Go, Rust, Ruby and PHP from apt;
  - `uv`, so PyPI MCP servers run with `uvx`.
- **Packages** install in exec form: names are arguments, never shell text, and a name starting with `-` fails plan.
- **Not enforced:** Docker can't enforce `network.allow` host lists or `resources.disk`, so plan warns about them. `resources.memory` becomes `docker run --memory`, defaulting to 8 GiB.
- **`image`:** a reference is used as the base. A Dockerfile is built first, tagged `solaria/<env>/<agent>-sandbox:<digest>`, and the generated Dockerfile starts from it. The environment's packages go on top in both cases.

### Consequences

- Good, because one package list produces both the cloud environment and the local image.
- Good, because developers get cloud-like sandboxes without writing Dockerfiles.
- Bad, because some tools can't be installed through these package managers (for example, NuGet isn't one of them). Those environments must use `image` and give up the cloud placement.
- Bad, because Solaria has to maintain a base image that tracks the cloud sandbox reference as it changes.
- Neutral: defaulting an omitted `network` to `limited` is stricter than Managed Agents' own default, deliberately.

### Confirmation

Golden tests cover Dockerfile generation, and capability tests confirm `cloud` rejects `image`.

## More information

- [Managed Agents: cloud environment setup](https://platform.claude.com/docs/en/managed-agents/environments)
- [Managed Agents: cloud sandbox reference](https://platform.claude.com/docs/en/managed-agents/cloud-sandboxes-reference)
- Spikes: [gitagent v3](../spikes/2026-11-gitagent-v3.md), [Managed Agents API](../spikes/2026-11-managed-agents-api.md)
- Revised 2026-11-04 after the M1 spikes: Ubuntu 24.04 base, sandbox base image, mapping table.
- Revised in M5: how the base image is built.
