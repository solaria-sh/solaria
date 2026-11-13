# Editors and coding agents

Solaria resources are YAML, and their JSON Schema is published at a stable address. Any editor with a YAML language server can use it for completion, hover text and field checks. For the checks that span files, such as references and paths, run `solaria validate`, from your editor or from a coding agent.

## Completion and field checks

The schema for every resource kind is at:

```text
https://solaria.sh/schemas/v1alpha1/solaria.json
```

It dispatches on `kind`, so the same address works for Agents, Targets and McpServers, and for files that hold several of them. With it, the [YAML language server](https://github.com/redhat-developer/yaml-language-server) (used by the VS Code YAML extension, Neovim, Helix, Zed and others) completes field names and allowed values, shows each field's description on hover, and marks unknown or mistyped fields.

Put this line at the top of each YAML file:

```yaml
# yaml-language-server: $schema=https://solaria.sh/schemas/v1alpha1/solaria.json
apiVersion: solaria.sh/v1alpha1
kind: Agent
```

Files named `solaria.yaml` or `solaria.yml` won't need the line once the schema is listed in [SchemaStore](https://www.schemastore.org/), which most YAML editors read. Solaria reads every YAML file in a project, so files with other names, such as `agent.yaml`, always need it.

The published schema follows Solaria's `main` branch, so it may list a field a little before a release ships it. To use the exact schema of your installed version, save it and point at the file instead:

```console
$ solaria schema > solaria.schema.json
```

```yaml
# yaml-language-server: $schema=./solaria.schema.json
```

## Checks across files

The schema checks one document at a time. `solaria validate` also checks what spans files: that every `ref:` names a resource that exists, that instruction files and skills are where the agent says, that overrides name agents the target has, and that `solaria.lock` matches the project. It never contacts a host, so it's quick and safe to run on every save.

`-o json` prints each diagnostic with its file, line and column:

```console
$ solaria validate -o json
```

Use it from an editor task, a pre-commit hook or CI. In VS Code, for example, a task in `.vscode/tasks.json` runs it with a problem matcher, which puts each error on its line:

```json
{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "solaria validate",
      "type": "shell",
      "command": "solaria validate",
      "problemMatcher": {
        "owner": "solaria",
        "fileLocation": ["relative", "${workspaceFolder}"],
        "pattern": {
          "regexp": "^(.+?):(\\d+):(\\d+): (error|warning): (.*)$",
          "file": 1,
          "line": 2,
          "column": 3,
          "severity": 4,
          "message": 5
        }
      }
    }
  ]
}
```

The text output has one diagnostic per line in that `file:line:column: severity: message` form.

## Coding agents

Coding agents that edit agent definitions should run the same check. Add a line like this to the project's `AGENTS.md`, `CLAUDE.md` or equivalent:

```markdown
After editing any Solaria resource (`apiVersion: solaria.sh/...`) or the files it names, run `solaria validate` and fix every error before you finish.
```

`solaria validate` names the file, line and field of each problem and suggests the field you meant, which is enough for an agent to fix it without help.
