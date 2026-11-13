# `stub` provider

The `stub` provider deploys to nothing but files. It runs the whole plan and apply cycle without a host, credentials or network, which makes it the provider for trying Solaria and for tests. It's Solaria's counterpart to Terraform's `null` provider (YAML reads an unquoted `null` as no value, hence the name).

```yaml
apiVersion: solaria.sh/v1alpha1
kind: Target
metadata: {name: local}
spec:
  provider: stub
  placement: file
```

## What apply does

Each agent becomes a JSON file under `.solaria/stub/<target>/`, holding its resolved definition. Editing or deleting one of those files simulates a change made outside Solaria: the next plan reports drift, or a deleted object to create again.

## Capabilities

`stub` accepts every model, MCP transport and sandbox form, and supports fallback models, approvals and output schemas. Destroy deletes the files.

## Settings

For tests, an agent can ask `stub` to fail: `providers.stub.fail: apply` or `destroy` makes that operation return an error, so partial applies can be exercised.
