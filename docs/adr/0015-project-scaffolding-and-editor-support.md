---
status: proposed
date: 2026-11-10
decision-makers: Renan Tarouco da Fonseca
---

# Project scaffolding and editor support

## Context and problem statement

A new project starts from an empty folder: the getting-started guide has people write `solaria.yaml` and `instructions.md` by hand. Teams that already have a project they like have no way to start the next one from it. In the editor, the JSON Schema gives field completion only after `solaria schema > file` and a modeline pointing at that file, because the URL in the schema's `$id` (`https://solaria.sh/schemas/v1alpha1/…`) isn't served yet (ADR 0003). Checks that span files, such as references, instruction and skill paths, and override keys, show up only when someone runs `solaria validate`.

How does a project start, and how much editor support does Solaria provide, and in what form?

## Decision drivers

- A new project validates and plans on the first try, with nothing installed beyond Solaria.
- Starting from a template never runs code from that template.
- A template is an ordinary Solaria project, so it can be validated, tested and reviewed like one.
- Editor support comes from standard tools (the YAML language server, SchemaStore) before Solaria ships its own.
- The same behavior in a terminal, in CI and in a coding agent: nothing prompts.

## Considered options

For starting a project:

- `solaria init` with templates embedded in the binary.
- `solaria init` with embedded templates and templates fetched from git.
- No command: the docs and `examples/` are the starting point.

For the editor:

- Serve the schema at its `$id` URLs, register `solaria.yaml` with SchemaStore, and document `solaria validate -o json` for editors and coding agents.
- The above, plus a language server, `solaria lsp`.

## Decision outcome

Chosen options: "`solaria init` with embedded templates and templates fetched from git", and "serve the schema, register it with SchemaStore, and document `validate -o json`", with the language server deferred.

### `solaria init`

- **Shape:** `solaria init [dir] [--template <name|source>]`. `dir` defaults to the current directory and is created if it doesn't exist. Flags only: init never prompts. An interactive prompt may come later as an addition, not a replacement.
- **Embedded templates:** compiled into the binary with `go:embed` from a package of their own, separate from `examples/`. Examples teach a concept, while templates start a project. They may overlap, but neither depends on the other. One template ships first and is the default: an Agent, `instructions.md`, and two Environments in a single `solaria.yaml`. The Environments are `local` (the `stub` provider, so `plan` and `apply` work with nothing installed) and `local-sdk` (`claude-agent-sdk` on the `process` placement, commented with what it needs to plan and run).
- **Git templates:** any `--template` value that isn't the name of an embedded template is a git source in the shape Terraform uses for module sources: `<git URL>[//<subdir>][?ref=<tag, branch or commit>]`, for example `https://github.com/solaria-sh/solaria//examples/reviewer?ref=v0.5.0`. init runs the `git` CLI for a shallow clone into a temporary directory, so the user's own git credentials and credential helpers apply, and Solaria never handles a token. Without `ref`, it takes the default branch. init prints the commit it copied from.
- **Verbatim copy:** a template is a runnable Solaria project and is copied as it is, with no variables and no renaming. Every file under the template's directory is copied except `.git/` and `.solaria/`. Users rename the agent afterwards.
- **Nothing runs:** init doesn't run hooks, scripts or anything else from a template. It reads files and writes files.
- **Validate first:** before writing anything, init runs the same checks as `solaria validate` on the template in its temporary directory. If they report errors, it prints them and writes nothing. Warnings are printed and don't stop it.
- **Existing files:** init works in a directory that already has files, such as an existing repository, as long as nothing would be overwritten. If any file it would write already exists, it names each one and writes nothing. `.gitignore` is the exception: init appends `.solaria/` when it's missing, or creates the file.
- **Schema modeline:** the embedded template's YAML files start with `# yaml-language-server: $schema=https://solaria.sh/schemas/v1alpha1/solaria.json`, so completion works whatever the files are later renamed to. Git templates are copied as they are and may or may not have one.
- **Output:** init lists the files it wrote, then the next commands: `solaria validate`, `solaria plan -e local`, `solaria apply -e local`.

### Editor support

- **Schema hosting:** the site build copies `schemas/v1alpha1/*.json` to `https://solaria.sh/schemas/v1alpha1/`, the URLs the `$id`s already name. It is built from `main`, like the rest of the site, so `schemas/**` joins the site workflow's triggers. An editor may offer a field that was added on `main` before the release that has it, which is acceptable while the API is `v1alpha1`. The files are served as JSON with `Access-Control-Allow-Origin: *`, so editors that run in a browser can fetch them. A new API version gets a new path, and old paths are never removed.
- **SchemaStore:** the catalog entry matches `solaria.yaml` and `solaria.yml` only. Solaria reads every YAML file in a project, but a broad pattern such as `agent.yaml` would claim other tools' files. Projects that split their resources across files rely on the modeline.
- **Cross-file checks in editors and coding agents:** the docs show how to run `solaria validate -o json` from an editor task or a coding agent's instructions after editing definitions. It's the same check CI runs, with file, line and column on every diagnostic.
- **Language server: deferred.** With the schema served, the YAML language server already provides field and enum completion, hover text from the field descriptions, and errors for unknown fields. What a language server would add is the cross-file checks inline as you type, and completion for `ref:` names and file paths. That needs `config.Load` to read unsaved editor buffers, and a VS Code extension to reach most users, which is more than this feedback is worth right now.

### Consequences

- Good, because any Solaria project in any git repository can be a template, including this repository's `examples/` at a release tag, without Solaria shipping it.
- Good, because a template that doesn't validate can't produce a broken project.
- Good, because a single-file project gets completion and hover in VS Code and other editors with no setup, once SchemaStore has the entry.
- Bad, because git templates need the `git` CLI on the machine.
- Bad, because template authors can't parameterize a template; users edit the result by hand.
- Bad, because the hosted schema follows `main`, not the user's installed release.
- Bad, because reference errors still appear only when `validate` runs, not as the user types.

### Confirmation

- A testscript runs `solaria init` with each embedded template, then `validate` and `plan -e local`, so the templates can't fall behind the schema.
- Testscripts cover a conflicting file, the `.gitignore` append, a template that fails validation, and a git template from a local bare repository (no network).
- The site's tests check that every file in `schemas/v1alpha1/` is served at its `$id`.

**As implemented** (`internal/scaffold`, `internal/cli/init.go`):

- The embedded template is `minimal`, and `-t` is short for `--template`. A value that looks like a name (lowercase letters, digits and dashes) is always an embedded template, so a misspelled name is an error rather than a git fetch.
- Git templates are fetched by `git init`, then `git fetch --depth 1 -- <url> <ref>`, then a detached checkout, instead of a shallow clone, because a fetch by name works for a branch, a tag or a commit alike. Without `ref` it fetches `HEAD`. Git runs with `GIT_TERMINAL_PROMPT=0`, so it fails instead of prompting, and with `protocol.ext.allow=never`, so a URL can't run a command. The `--` stops a URL from being read as an option.
- A template may hold only regular files and directories. A symbolic link is an error, so a template can't make init copy a file from outside it. Executable bits are kept.
- The `.gitignore` merge also adds the template's own `.gitignore` lines that the project's file doesn't have, under a `# Added by solaria init` comment.
- The next steps suggest the `local` environment when the template has one, or else its first environment.
- The git testscript uses an ordinary local repository through a `file://` URL, and is skipped on Windows and where git isn't installed.

## Pros and cons of the options

### `solaria init` with embedded templates only

- Good, because it needs no network and no git.
- Bad, because every new template needs a Solaria release, and teams can't share their own.

### No command, only docs and examples

- Good, because there's nothing to build.
- Bad, because the first experience is copying YAML by hand, and teams still have no way to start from their own projects.

### Template variables (text templating, or renaming the agent)

- Good, because the project starts with the user's names.
- Bad, because a templated file isn't a valid Solaria project, so templates can't be validated or tested as they are. Renaming only `metadata.name` needs rules for templates with several agents and for the references to them.

### A language server now

- Good, because reference errors and completion for `ref:` names appear as you type.
- Bad, because it needs unsaved-buffer support in config loading, a second server on every YAML file, and an editor extension to reach VS Code users. When it's picked up, the smallest version is `solaria lsp` over stdio, publishing `validate`'s diagnostics on open and save.

## More information

- [ADR 0003](0003-go-apache-2-and-distribution.md): distribution and the schema's stable URL.
- [ADR 0004](0004-resource-model.md): resources, file discovery and references.
- [ADR 0014](0014-process-placement-and-claude-agent-sdk.md): the `process` placement the default template uses.
- [Terraform module sources](https://developer.hashicorp.com/terraform/language/modules/sources), whose `//subdir` and `?ref=` syntax git templates follow.
- [SchemaStore](https://www.schemastore.org/) and the [YAML language server](https://github.com/redhat-developer/yaml-language-server) modeline.
