# Contributing to Solaria

Thanks for your interest. Solaria is in its design stage, so the most useful contributions right now are feedback on the [architecture decisions](docs/adr/) and issues describing hosts or agent setups you'd want supported.

## Development setup

You need Go 1.26 or newer (older `go` commands download the right toolchain automatically), `make`, and Docker for the container provider's integration tests.

```bash
git clone https://github.com/solaria-sh/solaria.git
cd solaria
make test
```

| Command | What it does |
|---|---|
| `make build` | Builds `bin/solaria` |
| `make test` | Runs the unit tests with the race detector |
| `make lint` | Runs golangci-lint (installed on demand with `go run`) |
| `make vuln` | Runs govulncheck |
| `make schema` | Regenerates `schemas/` from the Go types in `internal/api` |
| `make check` | Everything CI runs |

After changing a resource type, run `make schema` and commit the result; the tests fail otherwise. After an intended change to CLI output, run `go test ./internal/cli -update` and review the diff.

Unit tests never touch the network or real hosts. Integration tests that do are opt-in through environment variables, documented next to each test:

```console
$ SOLARIA_IT_DOCKER=1 go test ./internal/providers/gitagent -run Integration -v -timeout 50m
$ SOLARIA_IT_MANAGED_AGENTS=1 ANTHROPIC_API_KEY=... go test ./internal/providers/managedagents -run Integration -v
```

The Managed Agents test creates real objects named `solaria-it-*` in the key's organization and archives the agent at the end, which can't be undone.

## Making changes

1. For anything beyond a small fix, open an issue first so we can agree on the approach.
2. Decisions that shape the architecture get an [ADR](docs/adr/). Propose one in your pull request, starting from [the template](docs/adr/template.md).
3. Keep pull requests focused, and add tests for behavior changes.
4. Run `make check` before pushing.

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(engine): show replaced resources in plan output
fix(gitagent): quote paths with spaces in the Dockerfile
docs(adr): record the state backend decision
```

Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `build`, `ci`, `chore`.

## Releasing

Maintainers release by merging a release pull request. Merging to `main` on its own releases nothing. GoReleaser ([`.goreleaser.yaml`](.goreleaser.yaml)) builds the binaries for macOS, Linux and Windows, their checksums and SBOMs, the `ghcr.io/solaria-sh/solaria` image and the Homebrew cask. Cosign signs the checksums and the image keylessly.

To check a release locally, without signing or publishing anything:

```console
$ make release-check
$ make release-snapshot   # builds into dist/; needs Docker and syft
```

To release:

1. On every push to `main`, the [release-please workflow](.github/workflows/release-please.yml) keeps a pull request titled `chore: release X.Y.Z` open. It bumps the version from the commit types since the last release and adds their entries to `CHANGELOG.md`. Before 1.0, `feat` and breaking changes bump the minor version and `fix` bumps the patch.
2. When you want to release, review that pull request and merge it. release-please then pushes the `vX.Y.Z` tag and opens a **draft** GitHub release with the changelog entries as its notes.
3. The tag starts the [release workflow](.github/workflows/release.yml), which tests the tagged commit and attaches the archives, checksums, SBOMs and signatures to the draft, pushes the image and updates the tap. Review the draft's notes and assets, then publish it.

The changelog comes from commit messages, so fix an entry at its source rather than in `CHANGELOG.md`:

- To reword or drop an entry, edit the description of the merged pull request it came from and add the replacement messages between `BEGIN_COMMIT_OVERRIDE` and `END_COMMIT_OVERRIDE` lines. The release pull request picks it up on the next push to `main`, or when you re-run the workflow.
- To choose the version, push a commit with a `Release-As: X.Y.Z` footer: `git commit --allow-empty -s -m "chore: release X.Y.Z" -m "Release-As: X.Y.Z"`.

A tag pushed by hand (`git tag -s vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z`) still runs the release workflow, which then writes the release notes itself. Use it only if release-please is broken, and update `CHANGELOG.md` and `.release-please-manifest.json` to match.

The release workflow runs in the `release` environment, which only `v*` tags can use and which can require a reviewer. It needs a `HOMEBREW_TAP_GITHUB_TOKEN` secret: a fine-grained token with contents write access to `solaria-sh/homebrew-tap`.

release-please acts as a GitHub App, because a tag pushed with the workflow's own `GITHUB_TOKEN` doesn't start other workflows, and a pull request opened with it doesn't run CI. One-time setup:

1. Create a GitHub App owned by `solaria-sh` (organization settings, Developer settings, GitHub Apps), named for example `solaria-release`. Turn off its webhook. Give it these repository permissions and nothing else: **Contents: Read and write** (the release commit, the tag and the draft release), **Pull requests: Read and write** (the release pull request) and **Issues: Read and write** (its `autorelease` labels). Metadata: Read-only is added automatically. Let it be installed only on this account.
2. Generate a private key for the App, and install the App on `solaria-sh/solaria` only.
3. Save the App's client ID as the `RELEASE_APP_CLIENT_ID` repository variable, and the private key as the `RELEASE_APP_PRIVATE_KEY` repository secret.
4. If a ruleset protects `main` or `v*` tags, add the App to its bypass list, or allow it to create tags.

The repository setting "Allow GitHub Actions to create and approve pull requests" isn't needed: it only applies to `GITHUB_TOKEN`.

## The website

[solaria.sh](https://solaria.sh) is built from `site/` and `docs/` and hosted on Cloudflare Pages. The [site workflow](.github/workflows/site.yml) checks every link, deploys `main` to production, and gives each pull request from this repository a preview. Pull requests from forks are built and checked but not deployed.

One-time setup:

1. Create the Pages project with production branch `main`: `npx wrangler pages project create solaria --production-branch=main`. Then add the `solaria.sh` custom domain to it in the Cloudflare dashboard.
2. Create a Cloudflare API token with the **Cloudflare Pages: Edit** permission, and save it as the `CLOUDFLARE_API_TOKEN` repository secret.
3. Save the Cloudflare account ID as the `CLOUDFLARE_ACCOUNT_ID` repository variable. Set `CLOUDFLARE_PAGES_PROJECT` too if the project isn't named `solaria`.

## Developer Certificate of Origin

Every commit must be signed off, certifying that you wrote the change or have the right to submit it under the project's license (see the [DCO](https://developercertificate.org/)):

```bash
git commit -s -m "feat(cli): add status command"
```

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
