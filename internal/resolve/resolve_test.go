package resolve_test

import (
	"encoding/json"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renantarouco/solaria/internal/config"
	"github.com/renantarouco/solaria/internal/resolve"
	"github.com/renantarouco/solaria/internal/validate"
)

var update = flag.Bool("update", false, "rewrite golden files")

// project is a small valid project. Tests copy and change it.
var project = map[string]string{
	"agents.yaml": `apiVersion: solaria.sh/v1alpha1
kind: Agent
metadata:
  name: reviewer
  labels: {team: core}
spec:
  description: Reviews pull requests
  instructions: [role.md, rules.md]
  model:
    preferred: anthropic:claude-sonnet-5-5
    fallback: [anthropic:claude-haiku-5-5, openai:gpt-6-astra]
    effort: high
  tools:
    builtin: [shell, read, grep]
    mcp:
      - ref: git-host
        allowTools: [get_pull_request, add_comment]
  skills: [skills/review-rubric]
  permissions:
    ask: [shell, git-host/add_comment]
  limits: {maxTurns: 40, timeout: 90m}
  providers:
    managed-agents: {inference_geo: us, speed: standard}
---
apiVersion: solaria.sh/v1alpha1
kind: Agent
metadata: {name: helper}
spec:
  instructions: [role.md]
  model: {preferred: anthropic:claude-haiku-5-5}
`,
	"servers.yaml": `apiVersion: solaria.sh/v1alpha1
kind: McpServer
metadata: {name: git-host}
spec:
  remotes:
    - type: streamable-http
      url: https://git.example.com/mcp
      headers:
        - {name: Authorization, secretRef: GIT_HOST_TOKEN}
`,
	"environments.yaml": `apiVersion: solaria.sh/v1alpha1
kind: Environment
metadata: {name: local}
spec:
  provider: gitagent
  placement: docker
  overrides:
    reviewer:
      model: {preferred: anthropic:claude-haiku-5-5, fallback: []}
      limits: {maxTurns: 10}
      providers:
        managed-agents: {speed: null, inference_geo: eu}
        gitagent: {profile: fast}
---
apiVersion: solaria.sh/v1alpha1
kind: Environment
metadata: {name: cloud}
spec:
  provider: managed-agents
  placement: cloud
  agents:
    selector: {matchLabels: {team: core}}
`,
	"role.md":                               "You review pull requests.\n",
	"rules.md":                              "Never approve your own changes.\n",
	"skills/review-rubric/SKILL.md":         "---\nname: review-rubric\ndescription: How to grade a pull request.\n---\nGrade correctness first.\n",
	"skills/review-rubric/scripts/score.sh": "#!/bin/sh\necho 5\n",
}

func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// resolveAll writes files, checks they are valid, and resolves every
// environment.
func resolveAll(t *testing.T, files map[string]string) map[string][]resolve.Agent {
	t.Helper()
	dir := write(t, files)
	set, diags := config.Load([]string{dir})
	diags = append(diags, validate.Validate(set)...)
	if diags.HasErrors() {
		t.Fatalf("fixture is invalid:\n%v", diags)
	}
	out := map[string][]resolve.Agent{}
	for _, env := range []string{"local", "cloud"} {
		agents, err := resolve.Environment(set, env)
		if err != nil {
			t.Fatal(err)
		}
		out[env] = agents
	}
	return out
}

func find(t *testing.T, agents []resolve.Agent, name string) resolve.Agent {
	t.Helper()
	for _, a := range agents {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("agent %q not resolved", name)
	return resolve.Agent{}
}

// TestGolden pins the resolved definitions and their digests. A diff here
// means every deployed agent would get a new digest: that needs a new
// Format, not just -update.
func TestGolden(t *testing.T) {
	envs := resolveAll(t, project)
	type view struct {
		ID             string             `json:"id"`
		Provider       string             `json:"provider"`
		Placement      string             `json:"placement"`
		SpecDigest     string             `json:"specDigest"`
		ResolvedDigest string             `json:"resolvedDigest"`
		Definition     resolve.Definition `json:"definition"`
	}
	for env, agents := range envs {
		var views []view
		for _, a := range agents {
			views = append(views, view{a.ID(), a.Provider, a.Placement, a.SpecDigest, a.ResolvedDigest, a.Definition})
		}
		got, err := json.MarshalIndent(views, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		golden := filepath.Join("testdata", env+".golden.json")
		if *update {
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run go test -update to create it)", err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from the resolved agents:\n%s", golden, got)
		}
	}
}

func TestSelector(t *testing.T) {
	envs := resolveAll(t, project)
	if n := len(envs["local"]); n != 2 {
		t.Errorf("local resolved %d agents, want 2 (no selector)", n)
	}
	if h := find(t, envs["local"], "helper"); h.ResolvedDigest != h.SpecDigest {
		t.Error("helper has no override in local, so its digests must match")
	}
	if n := len(envs["cloud"]); n != 1 || envs["cloud"][0].Name != "reviewer" {
		t.Errorf("cloud resolved %v, want only reviewer (team: core)", envs["cloud"])
	}
}

func TestDigestsAcrossEnvironments(t *testing.T) {
	envs := resolveAll(t, project)
	local, cloud := find(t, envs["local"], "reviewer"), find(t, envs["cloud"], "reviewer")

	if local.SpecDigest != cloud.SpecDigest {
		t.Error("specDigest differs between environments; it must not depend on overrides or the provider")
	}
	if cloud.ResolvedDigest != cloud.SpecDigest {
		t.Error("without overrides, resolvedDigest must equal specDigest, whatever the provider")
	}
	if local.ResolvedDigest == local.SpecDigest {
		t.Error("overrides didn't change resolvedDigest")
	}
}

func TestOverrides(t *testing.T) {
	d := find(t, resolveAll(t, project)["local"], "reviewer").Definition
	if d.Model.Preferred != "anthropic:claude-haiku-5-5" || len(d.Model.Fallback) != 0 || d.Model.Effort != "high" {
		t.Errorf("model = %+v, want preferred and fallback replaced, effort kept", d.Model)
	}
	if d.Limits.MaxTurns != 10 || d.Limits.Timeout != "1h30m0s" {
		t.Errorf("limits = %+v, want maxTurns replaced, timeout kept and normalized", d.Limits)
	}
	ma := d.Providers["managed-agents"]
	if _, ok := ma["speed"]; ok || ma["inference_geo"] != "eu" {
		t.Errorf("managed-agents fields = %v, want speed removed by null and inference_geo replaced", ma)
	}
	if d.Providers["gitagent"]["profile"] != "fast" {
		t.Errorf("gitagent fields = %v, want the override's block added", d.Providers["gitagent"])
	}

	// The agent's own definition must be untouched by the override.
	spec := find(t, resolveAll(t, project)["cloud"], "reviewer").Definition
	if spec.Providers["managed-agents"]["speed"] != "standard" {
		t.Errorf("override leaked into the spec: %v", spec.Providers["managed-agents"])
	}
}

// TestDigestStability changes the project in ways that must not, and must,
// change the reviewer's specDigest.
func TestDigestStability(t *testing.T) {
	base := find(t, resolveAll(t, project)["cloud"], "reviewer").SpecDigest

	replace := func(file, old, repl string) func(map[string]string) {
		return func(f map[string]string) {
			if !strings.Contains(f[file], old) {
				t.Fatalf("fixture %s has no %q", file, old)
			}
			f[file] = strings.Replace(f[file], old, repl, 1)
		}
	}
	tests := []struct {
		name   string
		change func(map[string]string)
		same   bool
	}{
		{"keys reordered", replace("agents.yaml", "  description: Reviews pull requests\n  instructions: [role.md, rules.md]\n", "  instructions: [role.md, rules.md]\n  description: Reviews pull requests\n"), true},
		{"flow style", replace("agents.yaml", "limits: {maxTurns: 40, timeout: 90m}", "limits:\n    timeout: 90m\n    maxTurns: 40"), true},
		{"files merged and reordered", func(f map[string]string) {
			f["all.yaml"] = f["servers.yaml"] + "---\n" + f["agents.yaml"]
			delete(f, "servers.yaml")
			delete(f, "agents.yaml")
		}, true},
		{"builtin order", replace("agents.yaml", "[shell, read, grep]", "[grep, shell, read]"), true},
		{"allowTools order", replace("agents.yaml", "[get_pull_request, add_comment]", "[add_comment, get_pull_request]"), true},
		{"ask order", replace("agents.yaml", "[shell, git-host/add_comment]", "[git-host/add_comment, shell]"), true},
		{"equal timeout", replace("agents.yaml", "timeout: 90m", "timeout: 1h30m"), true},
		{"instruction file renamed", func(f map[string]string) {
			f["prompts/role.md"] = f["role.md"]
			replace("agents.yaml", "[role.md, rules.md]", "[prompts/role.md, rules.md]")(f)
		}, true},
		{"CRLF line endings", func(f map[string]string) {
			f["role.md"] = strings.ReplaceAll(f["role.md"], "\n", "\r\n")
			f["skills/review-rubric/SKILL.md"] = strings.ReplaceAll(f["skills/review-rubric/SKILL.md"], "\n", "\r\n")
		}, true},
		{"hidden file in skill", func(f map[string]string) { f["skills/review-rubric/.DS_Store"] = "\x00junk" }, true},
		{"secret value set", func(f map[string]string) { f[".env"] = "GIT_HOST_TOKEN=s3cret\n" }, true},

		{"skill byte changed", replace("skills/review-rubric/scripts/score.sh", "echo 5", "echo 6"), false},
		{"skill file added", func(f map[string]string) { f["skills/review-rubric/notes.md"] = "x" }, false},
		{"instruction order", replace("agents.yaml", "[role.md, rules.md]", "[rules.md, role.md]"), false},
		{"instruction text", replace("role.md", "pull requests", "merge requests"), false},
		{"fallback order", replace("agents.yaml", "[anthropic:claude-haiku-5-5, openai:gpt-6-astra]", "[openai:gpt-6-astra, anthropic:claude-haiku-5-5]"), false},
		{"MCP server changed", replace("servers.yaml", "git.example.com", "git.example.org"), false},
		{"secret reference renamed", replace("servers.yaml", "secretRef: GIT_HOST_TOKEN", "secretRef: GIT_TOKEN"), false},
		{"passthrough field", replace("agents.yaml", "inference_geo: us", "inference_geo: eu"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := maps.Clone(project)
			tt.change(files)
			got := find(t, resolveAll(t, files)["cloud"], "reviewer").SpecDigest
			if tt.same && got != base {
				t.Errorf("digest changed: %s -> %s", base, got)
			}
			if !tt.same && got == base {
				t.Errorf("digest unchanged (%s); this change must produce a new digest", got)
			}
		})
	}
}

func TestUnknownEnvironment(t *testing.T) {
	set, _ := config.Load([]string{write(t, project)})
	if _, err := resolve.Environment(set, "nowhere"); err == nil || !strings.Contains(err.Error(), `no Environment named "nowhere"`) {
		t.Errorf("err = %v, want no Environment named \"nowhere\"", err)
	}
}
