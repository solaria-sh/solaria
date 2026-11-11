# `gitagent` provider

The `gitagent` provider builds each agent into a container image that runs [gitagent](https://github.com/open-gitagent/gitagent) v3. The agent loop and its tools run in the same container, on your machine or wherever you run the image.

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Environment
metadata: {name: local-gitagent}
spec:
  provider: gitagent
  placement: docker
  sandbox:
    packages: {apt: [ripgrep]}
```

**Needs:** the `docker` CLI with BuildKit (Docker Desktop, OrbStack, Colima and Podman's docker alias all work) for `apply`. `plan` needs nothing until there's state to refresh. Running the image needs the model provider's API key.

## What apply does

1. Renders the agent into gitagent's config layer, `/root/.gitagent` in the image: `gitagent.yaml` (model, fallbacks, limits, MCP servers), `prompts/rules.md` (the instructions, which follow gitagent's own system prompt) and `skills/`.
2. Generates a Dockerfile: gitagent compiled from a pinned commit, the [Solaria sandbox base](../concepts.md#the-sandbox) or `sandbox.image`, then the environment's packages.
3. Builds `solaria/<environment>/<agent>:<resolvedDigest[:12]>`, labeled with the logical ID and both digests, and removes the previous tag.
4. Records a `docker run` command in the state. `apply` prints it, and `solaria status -e <environment> -a <agent>` shows it with the variables it needs; the task goes after it with `-p`. `status -o json` gives it as an `invocation` (see [Starting an agent](../concepts.md#starting-an-agent)).

The build context stays in `.solaria/gitagent/<environment>/<agent>/` for inspection. The first build compiles gitagent and the sandbox base, which takes several minutes; later builds reuse Docker's cache.

## How the definition maps

| Solaria | gitagent |
|---|---|
| `model.preferred`, `model.fallback` | `model.preferred`, `model.fallback` |
| `model.effort` | ignored, with a warning: gitagent sets reasoning through its model profile |
| `tools.builtin` | `shell`, `read`, `write` and `edit` map to gitagent's tools. `glob` and `grep` have none, so the agent searches through `shell`. `web_fetch` and `web_search` aren't available. Plan warns about each. |
| `tools.mcp` | `mcp_servers`. A remote is used when the server has one, so the agent reaches the same server it would on a cloud host; otherwise `npx` or `uvx` runs the package. |
| `allowTools` | `--allowed-tools` when every MCP ref lists `allowTools`, or `--disallowed-tools` for the built-ins the agent leaves out when none do. A mix can't be expressed, so plan fails. |
| `permissions.ask` | the tool is denied: nobody can approve during an unattended run |
| `limits` | `max_turns` and `timeout` |
| `output.schema` | not supported; plan fails |
| `secretRef` | `${NAME}` in `gitagent.yaml`; the run command passes the variable from the secret source |
| `telemetry` | `OTEL_*` variables in the run command. gitagent doesn't read `OTEL_RESOURCE_ATTRIBUTES` yet, so the identity and `resourceAttributes` set there don't reach its traces. |
| identity | `SOLARIA_LOGICAL_ID`, `SOLARIA_ENVIRONMENT`, `SOLARIA_SPEC_DIGEST` and `SOLARIA_RESOLVED_DIGEST` in the run command |

## Sandbox

- `packages` install in exec form: package names are arguments, never shell text, and a name starting with `-` fails plan.
- `resources.memory` becomes `docker run --memory`, 8 GiB by default.
- Docker can't enforce `network` host lists or `resources.disk`, so plan warns about them.
- `image` takes an image reference, or a Dockerfile path relative to the environment's file, which is built first.

## Settings

| Setting | Default | |
|---|---|---|
| `providers.gitagent.platform` | `linux/amd64` | The image platform. `linux/arm64` builds natively on Arm machines. gitagent cross-compiles on the build machine either way. |
| `providers.gitagent.commit` | `f4a309cd600b877177c17ac789e83bd7355d0383` | The gitagent commit to build, as a full 40-character hash. The default is the commit Solaria was tested with. |

The binding records the commit (output `gitagentCommit`, and runtime `gitagent@<commit>`, which `status -a` shows), since a model's results depend on the harness it runs in. Changing it plans an update although the agent's digests stay the same.

## Drift and destroy

The host ID is the image repository and the version is the image ID, so an image rebuilt or retagged outside Solaria shows as drift. Destroy removes the images.

## Limits

- The image runs as root, with `HOME=/root`.
- gitagent can't run MCP packages other than npm and PyPI.
- A telemetry header that comes from a secret can't reach gitagent's exporter yet, so it's left out with a warning.
- [Vault](../concepts.md#credentials-and-vaults) credentials reach the container under their secret's name, so an `env` credential must name its variable after its secret, or plan fails.
- With `secrets.source: dotenv`, docker passes the whole file to the container, so vaults don't limit which secrets the agent sees; plan warns. Use `secrets.source: env` to scope them.
