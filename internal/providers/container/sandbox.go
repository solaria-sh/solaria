package container

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

// Pinned inputs of the Solaria sandbox base. Changing one changes every
// image built on it, so the next plan rebuilds them.
const (
	// UbuntuImage is the cloud sandbox's OS (ADR 0007).
	UbuntuImage = "ubuntu:noble-20261024.1"
	nodeImage   = "node:24.22.2-bookworm-slim"
	uvVersion   = "0.13.7"
)

// WriteSandboxStages writes the two stages every Solaria container image
// shares: base, which is the Solaria sandbox base or the target's own
// image, and sandbox, which adds the target's packages. The image's own
// stage then starts FROM sandbox. Package names reach commands as exec-form
// arguments, never through a shell's parsing.
func WriteSandboxStages(b *strings.Builder, base string, p *v1alpha1.PackagesSpec) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	if base != "" {
		w("# The target's own sandbox image.\n")
		w("FROM %s AS base\n\n", base)
	} else {
		w("# The Solaria sandbox base: the cloud sandbox's OS and runtimes (ADR 0007).\n")
		w("FROM %s AS node\n\n", nodeImage)
		w("FROM %s AS base\n", UbuntuImage)
		w("ENV DEBIAN_FRONTEND=noninteractive\n")
		w("RUN apt-get update && apt-get install -y --no-install-recommends \\\n")
		w("      build-essential ca-certificates cargo curl git golang-go openjdk-21-jdk-headless \\\n")
		w("      php-cli python3 python3-pip python3-venv ruby-full rustc unzip xz-utils \\\n")
		w(" && rm -rf /var/lib/apt/lists/*\n")
		w("COPY --from=node /usr/local/bin/node /usr/local/bin/node\n")
		w("COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules\n")
		w("RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \\\n")
		w(" && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \\\n")
		w(" && pip install --no-cache-dir --break-system-packages uv==%s\n", uvVersion)
		w("ENV GOPATH=/opt/go GOBIN=/usr/local/bin CARGO_HOME=/opt/cargo PATH=/opt/cargo/bin:$PATH\n\n")
	}

	w("# The target's packages.\n")
	w("FROM base AS sandbox\n")
	if p != nil {
		run := func(cmd []string, pkgs []string) {
			if len(pkgs) == 0 {
				return
			}
			w("RUN %s\n", JSONArray(append(cmd, pkgs...)))
		}
		run([]string{"/bin/sh", "-c", `apt-get update && apt-get install -y --no-install-recommends "$@" && rm -rf /var/lib/apt/lists/*`, "apt"}, p.APT)
		run([]string{"cargo", "install", "--locked"}, p.Cargo)
		run([]string{"gem", "install", "--no-document"}, p.Gem)
		// go install takes one module per call when versions are given.
		run([]string{"/bin/sh", "-c", `for m in "$@"; do go install "$m"; done`, "go"}, p.Go)
		run([]string{"npm", "install", "--global"}, p.NPM)
		run([]string{"pip", "install", "--no-cache-dir", "--break-system-packages"}, p.Pip)
	}
	w("\n")
}

// SandboxBase returns the image the sandbox stage starts from: empty for
// the Solaria sandbox base, the target's image reference, or, for a
// Dockerfile, the tag repository:<hash> along with the build that makes it.
// The build must run before the image that uses it.
func SandboxBase(sb *v1alpha1.SandboxSpec, envDir, repository, platform string, labels map[string]string) (string, *BuildRequest, error) {
	if sb == nil || sb.Image == "" {
		return "", nil, nil
	}
	if !v1alpha1.IsDockerfilePath(sb.Image) {
		return sb.Image, nil, nil
	}
	file := filepath.Join(envDir, filepath.FromSlash(sb.Image))
	content, err := resolve.ReadText(file)
	if err != nil {
		return "", nil, fmt.Errorf("spec.image: %w", err)
	}
	tag := repository + ":" + strings.TrimPrefix(digest.Bytes(content), "sha256:")[:12]
	return tag, &BuildRequest{
		Context:    filepath.Dir(file),
		Dockerfile: file,
		Tag:        tag,
		Platform:   platform,
		Labels:     labels,
	}, nil
}

// Packages returns the sandbox's packages, or nil.
func Packages(sb *v1alpha1.SandboxSpec) *v1alpha1.PackagesSpec {
	if sb == nil {
		return nil
	}
	return sb.Packages
}

// OptionLikePackages lists package names that start with '-', which a
// package manager would read as an option.
func OptionLikePackages(sb *v1alpha1.SandboxSpec) []string {
	p := Packages(sb)
	if p == nil {
		return nil
	}
	var out []string
	for _, list := range [][]string{p.APT, p.Cargo, p.Gem, p.Go, p.NPM, p.Pip} {
		for _, pkg := range list {
			if strings.HasPrefix(pkg, "-") {
				out = append(out, pkg)
			}
		}
	}
	return out
}

// Memory converts sandbox.resources.memory to docker's units, defaulting
// to the cloud sandbox's 8 GiB (ADR 0007).
func Memory(sb *v1alpha1.SandboxSpec) string {
	if sb == nil || sb.Resources == nil || sb.Resources.Memory == "" {
		return "8g"
	}
	m := sb.Resources.Memory
	for suffix, unit := range map[string]string{"Ki": "k", "Mi": "m", "Gi": "g"} {
		if n, ok := strings.CutSuffix(m, suffix); ok {
			return n + unit
		}
	}
	if n, ok := strings.CutSuffix(m, "Ti"); ok {
		v, _ := strconv.Atoi(n)
		return strconv.Itoa(v*1024) + "g"
	}
	return m + "b"
}

// JSONArray encodes an exec-form instruction's arguments, leaving & < >
// readable.
func JSONArray(args []string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(args) // strings always encode
	return strings.TrimSuffix(b.String(), "\n")
}

// ShellCommand joins argv into a command for a POSIX shell run from the
// workspace: each argument quoted when it needs it, and
// state.WorkspacePlaceholder as "$PWD".
func ShellCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		pieces := strings.Split(arg, state.WorkspacePlaceholder)
		for j, p := range pieces {
			if p != "" || len(pieces) == 1 {
				pieces[j] = ShellQuote(p)
			}
		}
		parts[i] = strings.Join(pieces, `"$PWD"`)
	}
	return strings.Join(parts, " ")
}

// ShellQuote quotes s for a POSIX shell when it needs it.
func ShellQuote(s string) string {
	safe := func(c rune) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_./:=,@%+", c)
	}
	if s != "" && strings.IndexFunc(s, func(c rune) bool { return !safe(c) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// WorkspaceVolume is the docker run --volume value that mounts an agent's
// workspace at /workspace: the folder the target binds, or the one
// the run starts in through state.WorkspacePlaceholder, read-only when the
// agent may not change it (ADR 0017).
func WorkspaceVolume(a *resolve.Agent) string {
	src := a.WorkspacePath()
	if src == "" {
		src = state.WorkspacePlaceholder
	}
	v := src + ":/workspace"
	if a.Definition.ReadOnlyWorkspace() {
		v += ":ro"
	}
	return v
}
