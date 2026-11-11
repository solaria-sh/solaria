package managedagents

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/engine"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/providers/container"
	"github.com/solaria-sh/solaria/internal/state"
	"github.com/solaria-sh/solaria/internal/validate"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fakeAPI is an in-memory Managed Agents API. It keeps a transcript of
// the requests it receives, which the tests compare with golden files.
type fakeAPI struct {
	url        string
	mu         sync.Mutex
	transcript strings.Builder
	seq        int
	agents     map[string]*fakeAgent
	envs       map[string]*fakeEnv
	skills     map[string][]string // skill ID → version IDs
	vaults     map[string]*fakeVault
}

type fakeVault struct {
	name     string
	archived bool
	creds    map[string]*fakeCred
}

// fakeCred keeps the value it was sent, which the transcript hides.
type fakeCred struct {
	auth    map[string]any
	value   string
	updated int
}

type fakeAgent struct {
	version  int64
	archived bool
}

type fakeEnv struct {
	updated  int
	archived bool
	// inUse makes delete fail, as when sessions refer to it.
	inUse bool
}

func newFakeAPI(t *testing.T) (*fakeAPI, anthropic.Client) {
	f := &fakeAPI{agents: map[string]*fakeAgent{}, envs: map[string]*fakeEnv{}, skills: map[string][]string{}, vaults: map[string]*fakeVault{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	client := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test-key"), option.WithMaxRetries(0))
	return f, client
}

func (f *fakeAPI) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s_%02d", prefix, f.seq)
}

func (f *fakeAPI) take() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.transcript.String()
	f.transcript.Reset()
	return s
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, err := f.record(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/agents") || strings.HasPrefix(r.URL.Path, "/v1/environments") || strings.HasPrefix(r.URL.Path, "/v1/vaults") {
		if !strings.Contains(r.Header.Get("anthropic-beta"), "managed-agents-2026-04-01") {
			fail(w, http.StatusBadRequest, "missing the managed-agents beta header")
			return
		}
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[1:]
	route := r.Method + " " + strings.Join(pattern(parts), "/")
	switch route {
	case "POST agents":
		id := f.id("agent")
		f.agents[id] = &fakeAgent{version: 1}
		reply(w, agentJSON(id, f.agents[id]))
	case "GET agents/*", "POST agents/*", "POST agents/*/archive":
		a := f.agents[parts[1]]
		if a == nil {
			fail(w, http.StatusNotFound, "no such agent")
			return
		}
		switch {
		case r.Method == http.MethodPost && len(parts) == 3:
			if a.archived {
				fail(w, http.StatusBadRequest, "agent is already archived")
				return
			}
			a.archived = true
		case r.Method == http.MethodPost:
			var req struct{ Version int64 }
			_ = json.Unmarshal(body, &req)
			if req.Version != a.version {
				fail(w, http.StatusConflict, fmt.Sprintf("version %d is not the latest (%d)", req.Version, a.version))
				return
			}
			a.version++
		}
		reply(w, agentJSON(parts[1], a))
	case "POST environments":
		id := f.id("env")
		f.envs[id] = &fakeEnv{updated: f.seq}
		reply(w, envJSON(id, f.envs[id]))
	case "GET environments/*", "POST environments/*", "DELETE environments/*", "POST environments/*/archive":
		e := f.envs[parts[1]]
		if e == nil {
			fail(w, http.StatusNotFound, "no such environment")
			return
		}
		switch {
		case r.Method == http.MethodDelete && e.inUse:
			fail(w, http.StatusConflict, "sessions refer to this environment")
			return
		case r.Method == http.MethodDelete:
			delete(f.envs, parts[1])
			reply(w, map[string]any{"id": parts[1], "type": "environment_deleted"})
			return
		case len(parts) == 3:
			e.archived = true
		case r.Method == http.MethodPost:
			f.seq++
			e.updated = f.seq
		}
		reply(w, envJSON(parts[1], e))
	case "POST skills":
		id := f.id("skill")
		v := f.id("skillver")
		f.skills[id] = []string{v}
		reply(w, map[string]any{"id": id, "latest_version_id": v, "display_name": "triage", "type": "skill"})
	case "POST skills/*/versions", "GET skills/*/versions":
		vs, ok := f.skills[parts[1]]
		if !ok {
			fail(w, http.StatusNotFound, "no such skill")
			return
		}
		if r.Method == http.MethodGet {
			var data []map[string]any
			for _, v := range vs {
				data = append(data, map[string]any{"id": v, "skill_id": parts[1], "type": "skill_version"})
			}
			reply(w, map[string]any{"data": data, "next_page": nil})
			return
		}
		v := f.id("skillver")
		f.skills[parts[1]] = append(vs, v)
		reply(w, map[string]any{"id": v, "skill_id": parts[1], "type": "skill_version"})
	case "DELETE skills/*/versions/*":
		vs := f.skills[parts[1]]
		if !slices.Contains(vs, parts[3]) {
			fail(w, http.StatusNotFound, "no such skill version")
			return
		}
		f.skills[parts[1]] = slices.DeleteFunc(vs, func(v string) bool { return v == parts[3] })
		reply(w, map[string]any{"id": parts[3], "type": "skill_version_deleted"})
	case "DELETE skills/*":
		if len(f.skills[parts[1]]) > 0 {
			fail(w, http.StatusBadRequest, "delete the skill's versions first")
			return
		}
		delete(f.skills, parts[1])
		reply(w, map[string]any{"id": parts[1], "type": "skill_deleted"})
	case "POST vaults":
		var req struct {
			DisplayName string `json:"display_name"`
		}
		_ = json.Unmarshal(body, &req)
		id := f.id("vlt")
		f.vaults[id] = &fakeVault{name: req.DisplayName, creds: map[string]*fakeCred{}}
		reply(w, vaultJSON(id, f.vaults[id]))
	case "GET vaults/*", "POST vaults/*", "DELETE vaults/*":
		v := f.vaults[parts[1]]
		if v == nil {
			fail(w, http.StatusNotFound, "no such vault")
			return
		}
		switch r.Method {
		case http.MethodDelete:
			delete(f.vaults, parts[1])
			reply(w, map[string]any{"id": parts[1], "type": "vault_deleted"})
			return
		case http.MethodPost:
			var req struct {
				DisplayName string `json:"display_name"`
			}
			_ = json.Unmarshal(body, &req)
			v.name = req.DisplayName
		}
		reply(w, vaultJSON(parts[1], v))
	case "POST vaults/*/credentials", "GET vaults/*/credentials":
		v := f.vaults[parts[1]]
		if v == nil {
			fail(w, http.StatusNotFound, "no such vault")
			return
		}
		if r.Method == http.MethodGet {
			data := []map[string]any{}
			for _, id := range slices.Sorted(maps.Keys(v.creds)) {
				data = append(data, credJSON(id, parts[1], v.creds[id]))
			}
			reply(w, map[string]any{"data": data, "next_page": nil})
			return
		}
		var req struct{ Auth map[string]any }
		_ = json.Unmarshal(body, &req)
		id := f.id("vcrd")
		c := &fakeCred{auth: req.Auth, updated: f.seq}
		c.value = takeValue(c.auth)
		v.creds[id] = c
		reply(w, credJSON(id, parts[1], c))
	case "POST vaults/*/credentials/*", "DELETE vaults/*/credentials/*":
		v := f.vaults[parts[1]]
		if v == nil || v.creds[parts[3]] == nil {
			fail(w, http.StatusNotFound, "no such credential")
			return
		}
		c := v.creds[parts[3]]
		if r.Method == http.MethodDelete {
			delete(v.creds, parts[3])
			reply(w, map[string]any{"id": parts[3], "type": "vault_credential_deleted"})
			return
		}
		var req struct{ Auth map[string]any }
		_ = json.Unmarshal(body, &req)
		if req.Auth["type"] != c.auth["type"] {
			fail(w, http.StatusBadRequest, "a credential's type can't change")
			return
		}
		if val := takeValue(req.Auth); val != "" {
			c.value = val
		}
		f.seq++
		c.updated = f.seq
		reply(w, credJSON(parts[3], parts[1], c))
	default:
		fail(w, http.StatusNotFound, "no route "+route)
	}
}

// takeValue removes a credential's value from its auth, as the host never
// returns it.
func takeValue(auth map[string]any) string {
	for _, k := range []string{"token", "secret_value"} {
		if v, ok := auth[k].(string); ok {
			delete(auth, k)
			return v
		}
	}
	return ""
}

func vaultJSON(id string, v *fakeVault) map[string]any {
	m := map[string]any{
		"id": id, "type": "vault", "display_name": v.name, "metadata": map[string]any{},
		"created_at": "2026-11-05T12:00:00Z", "updated_at": "2026-11-05T12:00:00Z", "archived_at": nil,
	}
	if v.archived {
		m["archived_at"] = "2026-11-05T12:59:59Z"
	}
	return m
}

func credJSON(id, vault string, c *fakeCred) map[string]any {
	return map[string]any{
		"id": id, "type": "vault_credential", "vault_id": vault, "auth": c.auth, "metadata": map[string]any{},
		"created_at": "2026-11-05T12:00:00Z", "updated_at": fmt.Sprintf("2026-11-05T12:00:%02dZ", c.updated), "archived_at": nil,
	}
}

// pattern replaces IDs in a path with *.
func pattern(parts []string) []string {
	out := slices.Clone(parts)
	for i := 1; i < len(out); i += 2 {
		out[i] = "*"
	}
	return out
}

// record writes a request to the transcript: its method and path, then its
// JSON body with sorted keys, or a multipart upload's fields and files.
func (f *fakeAPI) record(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&f.transcript, "%s %s\n", r.Method, r.URL.Path)
	ctype, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case ctype == "multipart/form-data":
		mr := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			content, _ := io.ReadAll(part)
			if part.FormName() != "files[]" {
				fmt.Fprintf(&f.transcript, "  field %s: %s\n", part.FormName(), content)
				continue
			}
			// Part.FileName drops folders, which the upload relies on.
			_, disp, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			fmt.Fprintf(&f.transcript, "  file %s %s (%s):\n", part.FormName(), disp["filename"], part.Header.Get("Content-Type"))
			sc := bufio.NewScanner(strings.NewReader(string(content)))
			for sc.Scan() {
				fmt.Fprintf(&f.transcript, "    | %s\n", sc.Text())
			}
		}
	case len(body) > 0:
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, err
		}
		hideValues(v)
		pretty, _ := json.MarshalIndent(v, "  ", "  ")
		fmt.Fprintf(&f.transcript, "  %s\n", pretty)
	}
	return body, nil
}

// hideValues replaces credential values in a request body with ***, so
// the transcript shows that one was sent but not what it was.
func hideValues(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, val := range m {
		if s, ok := val.(string); ok && (k == "token" || k == "secret_value") && s != "" {
			m[k] = "***"
		}
		hideValues(val)
	}
}

func agentJSON(id string, a *fakeAgent) map[string]any {
	m := map[string]any{"id": id, "type": "agent", "version": a.version, "archived_at": nil}
	if a.archived {
		m["archived_at"] = "2026-11-05T12:00:00Z"
	}
	return m
}

func envJSON(id string, e *fakeEnv) map[string]any {
	m := map[string]any{"id": id, "type": "environment", "updated_at": fmt.Sprintf("2026-11-05T12:00:%02dZ", e.updated), "archived_at": nil}
	if e.archived {
		m["archived_at"] = "2026-11-05T12:59:59Z"
	}
	return m
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": msg}})
}

// fakeDocker records docker calls and keeps containers by name.
type fakeDocker struct {
	calls      []string
	containers map[string]*container.Container
}

func (d *fakeDocker) Build(_ context.Context, req container.BuildRequest, _ io.Writer) error {
	d.calls = append(d.calls, fmt.Sprintf("build %s --platform %s", req.Tag, req.Platform))
	return nil
}

func (d *fakeDocker) Inspect(context.Context, string) (*container.Image, error) {
	return &container.Image{ID: "sha256:image"}, nil
}

func (d *fakeDocker) Remove(_ context.Context, ref string) error {
	d.calls = append(d.calls, "rmi "+ref)
	return nil
}

func (d *fakeDocker) RunDetached(_ context.Context, req container.RunRequest) (string, error) {
	var secrets []string
	for _, k := range slices.Sorted(maps.Keys(req.Secrets)) {
		secrets = append(secrets, k+"="+strings.Repeat("*", len(req.Secrets[k])))
	}
	d.calls = append(d.calls, fmt.Sprintf("run %s %s --platform %s --memory %s env=%v secrets=%v",
		req.Name, req.Image, req.Platform, req.Memory, req.Env, secrets))
	d.containers[req.Name] = &container.Container{ID: "c1", Image: req.Image, Running: true}
	return "c1", nil
}

func (d *fakeDocker) InspectContainer(_ context.Context, name string) (*container.Container, error) {
	return d.containers[name], nil
}

func (d *fakeDocker) RemoveContainer(_ context.Context, name string) error {
	d.calls = append(d.calls, "rm "+name)
	delete(d.containers, name)
	return nil
}

// project copies the fixture to a temporary folder the test can edit.
func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "project")))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

type rig struct {
	t      *testing.T
	dir    string
	api    *fakeAPI
	docker *fakeDocker
	client anthropic.Client
	state  *state.Local
	golden strings.Builder
}

func newRig(t *testing.T) *rig {
	api, client := newFakeAPI(t)
	dir := project(t)
	return &rig{
		t: t, dir: dir, api: api, client: client,
		docker: &fakeDocker{containers: map[string]*container.Container{}},
		state:  &state.Local{Dir: filepath.Join(dir, ".solaria", "state")},
	}
}

func (r *rig) engine() *engine.Engine {
	r.t.Helper()
	set, diags := config.Load([]string{r.dir})
	diags = append(diags, validate.Validate(set)...)
	if diags.HasErrors() {
		r.t.Fatalf("invalid fixture:\n%s", diags)
	}
	return &engine.Engine{
		Set:     set,
		Backend: r.state,
		WorkDir: r.dir,
		NewProvider: func(_ string, opts provider.Options) (provider.Provider, error) {
			return NewWithClient(opts, r.client, r.docker), nil
		},
		Now: func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) },
	}
}

// step plans and applies env, writing the rendered plan, the requests and
// the docker calls to the golden transcript.
func (r *rig) step(title, env string, opts engine.PlanOptions) error {
	r.t.Helper()
	e := r.engine()
	pl, err := e.Plan(context.Background(), env, opts)
	if err != nil {
		r.t.Fatalf("%s: plan: %v", title, err)
	}
	var b strings.Builder
	if err := engine.Render(&b, pl); err != nil {
		r.t.Fatal(err)
	}
	refresh := r.api.take()
	r.docker.calls = nil
	applyErr := e.Apply(context.Background(), pl)
	fmt.Fprintf(&r.golden, "### %s\n--- plan\n%s", title, strings.ReplaceAll(b.String(), r.dir, "$PROJECT"))
	if refresh != "" {
		fmt.Fprintf(&r.golden, "--- refresh requests\n%s", refresh)
	}
	fmt.Fprintf(&r.golden, "--- apply requests\n%s", r.api.take())
	if len(r.docker.calls) > 0 {
		fmt.Fprintf(&r.golden, "--- docker\n%s\n", strings.Join(r.docker.calls, "\n"))
	}
	if applyErr != nil {
		fmt.Fprintf(&r.golden, "--- error\n%s\n", strings.ReplaceAll(applyErr.Error(), r.api.url, "$API"))
	}
	r.golden.WriteString("\n")
	return applyErr
}

func (r *rig) edit(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) check(name string) {
	r.t.Helper()
	got := r.golden.String()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	if got != string(want) {
		r.t.Errorf("transcript differs from %s; run go test -update to see how:\n%s", path, got)
	}
}

func (r *rig) agentID() string {
	r.t.Helper()
	st, err := r.state.Read("ma-cloud")
	if err != nil {
		r.t.Fatal(err)
	}
	return st.Agents["Agent/reviewer"].Binding.HostID
}

// TestCloudLifecycle drives the engine through a cloud environment's life
// and compares every request with testdata/cloud.golden.
func TestCloudLifecycle(t *testing.T) {
	r := newRig(t)
	refresh := engine.PlanOptions{Refresh: true}
	if err := r.step("create", "ma-cloud", refresh); err != nil {
		t.Fatal(err)
	}
	if err := r.step("unchanged", "ma-cloud", refresh); err != nil {
		t.Fatal(err)
	}

	r.edit("role.md", "You review pull requests for correctness and tests.\n")
	r.edit("skills/triage/SKILL.md", "---\nname: triage\ndescription: Sort review findings by severity.\n---\n\nSort findings: blocking first.\n")
	if err := r.step("edit the instructions and the skill", "ma-cloud", refresh); err != nil {
		t.Fatal(err)
	}

	// Someone edits the agent in the Console: version 2 becomes 3.
	r.api.agents[r.agentID()].version++
	if err := r.step("changed outside Solaria", "ma-cloud", refresh); err != nil {
		t.Fatal(err)
	}

	// A plan made without refresh doesn't know about a later change, and
	// the update is refused rather than overwriting it.
	r.api.agents[r.agentID()].version++
	r.edit("role.md", "You review pull requests.\n")
	if err := r.step("stale plan", "ma-cloud", engine.PlanOptions{}); err == nil || !strings.Contains(err.Error(), "plan again, with refresh") {
		t.Errorf("stale apply: err = %v", err)
	}

	// The skill is dropped from the card.
	r.edit("solaria.yaml", strings.Replace(readFile(t, filepath.Join(r.dir, "solaria.yaml")), "  skills: [skills/triage]\n", "", 1))
	if err := r.step("drop the skill", "ma-cloud", refresh); err != nil {
		t.Fatal(err)
	}

	r.api.envs[stHost(t, r).Binding.HostID].inUse = true
	if err := r.step("destroy", "ma-cloud", engine.PlanOptions{Refresh: true, Destroy: true}); err != nil {
		t.Fatal(err)
	}
	if len(r.api.skills) != 0 {
		t.Errorf("skills left on the host: %v", r.api.skills)
	}
	r.check("cloud")
}

// TestSelfHostedLifecycle covers the worker: the first apply stops for the
// environment key, the next one starts the worker, and destroy removes it.
func TestSelfHostedLifecycle(t *testing.T) {
	t.Setenv("SOLARIA_TEST_ENVIRONMENT_KEY", "")
	r := newRig(t)
	refresh := engine.PlanOptions{Refresh: true}
	err := r.step("create without a key", "ma-self-hosted", refresh)
	if err == nil || !strings.Contains(err.Error(), "SOLARIA_TEST_ENVIRONMENT_KEY") {
		t.Fatalf("first apply: err = %v", err)
	}

	t.Setenv("SOLARIA_TEST_ENVIRONMENT_KEY", "envkey-secret")
	if err := r.step("start the worker", "ma-self-hosted", refresh); err != nil {
		t.Fatal(err)
	}
	if err := r.step("unchanged", "ma-self-hosted", refresh); err != nil {
		t.Fatal(err)
	}
	delete(r.docker.containers, workerName("ma-self-hosted"))
	if err := r.step("worker stopped", "ma-self-hosted", refresh); err != nil {
		t.Fatal(err)
	}
	if err := r.step("destroy", "ma-self-hosted", engine.PlanOptions{Refresh: true, Destroy: true}); err != nil {
		t.Fatal(err)
	}
	if len(r.api.envs) != 0 || len(r.docker.containers) != 0 {
		t.Errorf("left over: environments %v, containers %v", r.api.envs, r.docker.containers)
	}
	if strings.Contains(r.golden.String(), "envkey-secret") {
		t.Error("the environment key reached the transcript")
	}
	r.check("self-hosted")
}

func TestWorkerDockerfileGolden(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("testdata", "project"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t)
	r.dir = dir
	set, _ := config.Load([]string{dir})
	env := set.Lookup("Environment", "ma-self-hosted").Environment()
	w, err := renderWorker(&provider.Environment{Name: "ma-self-hosted", Spec: &env.Spec, Dir: dir}, settings{platform: "linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}
	r.golden.Write(w.files["Dockerfile"])
	fmt.Fprintf(&r.golden, "=== image\n%s\n", w.image)
	r.check("worker-dockerfile")
}

// TestWorkerModuleMatches keeps the worker's SDK version in step with
// Solaria's, so the embedded go.sum stays valid.
func TestWorkerModuleMatches(t *testing.T) {
	want := sdkRequire(t, readFile(t, filepath.Join("..", "..", "..", "go.mod")))
	if got := sdkRequire(t, string(workerGoMod)); got != want {
		t.Errorf("worker/go.mod.txt requires %s, Solaria %s; regenerate it (see worker/main.go)", got, want)
	}
}

func sdkRequire(t *testing.T, gomod string) string {
	t.Helper()
	for _, line := range strings.Split(gomod, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && slices.Contains(f, "github.com/anthropics/anthropic-sdk-go/v2") {
			return f[len(f)-1]
		}
	}
	t.Fatal("no anthropic-sdk-go requirement")
	return ""
}

func TestValidateEnvironment(t *testing.T) {
	r := newRig(t)
	r.edit("solaria.yaml", strings.NewReplacer(
		"packageManagers: true}", "packageManagers: false}\n    resources: {memory: 16Gi, disk: 2Gi}",
		"  providers:\n    managed-agents: {namePrefix: solaria-test-}", "  telemetry: {endpoint: \"http://localhost:4318\"}\n  providers:\n    managed-agents: {namePrefix: solaria-test-, platform: linux/arm64, colour: blue}",
	).Replace(readFile(t, filepath.Join(r.dir, "solaria.yaml"))))
	_, err := r.engine().Plan(context.Background(), "ma-cloud", engine.PlanOptions{})
	var failed *engine.FailedError
	if !errors.As(err, &failed) {
		t.Fatalf("plan: err = %v, want the errors below", err)
	}
	got := failed.Diagnostics.String()
	for _, want := range []string{
		"error: sandbox.packages need sandbox.network.packageManagers: true",
		"error: sandbox.resources.memory is 16Gi, above the cloud sandbox's fixed 8Gi",
		"warning: sandbox.resources.disk is ignored",
		"warning: telemetry is ignored",
		"warning: providers.managed-agents.colour isn't a setting",
		"warning: providers.managed-agents.platform only applies to the self-hosted placement",
		"warning: sandbox.network is limited, so web_fetch and web_search reach only sandbox.network.allow hosts (used by reviewer)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostics lack %q:\n%s", want, got)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func stHost(t *testing.T, r *rig) *state.EnvironmentEntry {
	t.Helper()
	st, err := r.state.Read("ma-cloud")
	if err != nil {
		t.Fatal(err)
	}
	return st.Host
}

// vaultProject adds a Vault to the fixture: a bearer token for the docs
// server and a variable for a CLI.
func vaultProject(r *rig) {
	r.t.Helper()
	y := readFile(r.t, filepath.Join(r.dir, "solaria.yaml"))
	y = strings.Replace(y, "  permissions: {ask: [shell]}\n", "  permissions: {ask: [shell]}\n  vaults: [{ref: tools}]\n", 1)
	y = strings.Replace(y, "      url: https://docs.example.com/mcp\n", "      url: https://docs.example.com/mcp\n      headers: [{name: Authorization, secretRef: DOCS_TOKEN}]\n", 1)
	y += `---
apiVersion: solaria.sh/v1alpha1
kind: Vault
metadata: {name: tools}
spec:
  credentials:
    - name: docs
      secretRef: DOCS_TOKEN
      for: {mcpServer: {ref: docs}}
    - name: gh
      secretRef: GH_PAT
      for: {env: GH_TOKEN}
`
	r.edit("solaria.yaml", y)
}

// values returns the values the fake host holds, by credential display
// order (the vault's credentials sorted by ID).
func (r *rig) values() []string {
	r.t.Helper()
	var out []string
	for _, v := range r.api.vaults {
		for _, id := range slices.Sorted(maps.Keys(v.creds)) {
			out = append(out, v.creds[id].value)
		}
	}
	return out
}

// TestVaultLifecycle drives a host vault through its life (ADR 0016) and
// compares every request with testdata/vault.golden. Values reach the
// host, never the transcript, plan or state.
func TestVaultLifecycle(t *testing.T) {
	t.Setenv("DOCS_TOKEN", "Bearer docs-secret-1")
	t.Setenv("GH_PAT", "gh-secret-1")
	r := newRig(t)
	vaultProject(r)
	refresh := engine.PlanOptions{Refresh: true}
	if err := r.step("create", "ma-cloud", refresh); err != nil {
		t.Fatal(err)
	}
	// The host adds "Bearer " to an MCP token itself.
	if got := r.values(); !slices.Equal(got, []string{"docs-secret-1", "gh-secret-1"}) {
		t.Errorf("values after create: %q", got)
	}
	if err := r.step("unchanged", "ma-cloud", refresh); err != nil {
		t.Fatal(err)
	}

	// State can't see a changed value; --rotate-secrets sends them again.
	t.Setenv("DOCS_TOKEN", "docs-secret-2")
	t.Setenv("GH_PAT", "gh-secret-2")
	if err := r.step("rotate", "ma-cloud", engine.PlanOptions{Refresh: true, RotateSecrets: true}); err != nil {
		t.Fatal(err)
	}
	if got := r.values(); !slices.Equal(got, []string{"docs-secret-2", "gh-secret-2"}) {
		t.Errorf("values after rotate: %q", got)
	}

	// A new secret for the same target updates the credential; a new
	// variable needs a new credential.
	t.Setenv("GH_PAT_2", "gh-secret-3")
	y := readFile(t, filepath.Join(r.dir, "solaria.yaml"))
	y = strings.Replace(y, "secretRef: GH_PAT\n      for: {env: GH_TOKEN}", "secretRef: GH_PAT_2\n      for: {env: GITHUB_TOKEN}", 1)
	r.edit("solaria.yaml", y)
	if err := r.step("change the variable and its secret", "ma-cloud", refresh); err != nil {
		t.Fatal(err)
	}

	// A missing secret stops apply before any host call.
	t.Setenv("DOCS_TOKEN", "")
	if err := os.Unsetenv("DOCS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	err := r.step("rotate without a secret", "ma-cloud", engine.PlanOptions{Refresh: true, RotateSecrets: true})
	if err == nil || !strings.Contains(err.Error(), "secrets not set in the environment variables: DOCS_TOKEN") {
		t.Errorf("rotate without a secret: err = %v", err)
	}
	t.Setenv("DOCS_TOKEN", "docs-secret-2")

	// Someone deletes the vault in the Console.
	for id := range r.api.vaults {
		delete(r.api.vaults, id)
	}
	if err := r.step("deleted outside Solaria", "ma-cloud", refresh); err != nil {
		t.Fatal(err)
	}

	if err := r.step("destroy", "ma-cloud", engine.PlanOptions{Refresh: true, Destroy: true}); err != nil {
		t.Fatal(err)
	}
	if len(r.api.vaults) != 0 {
		t.Errorf("vaults left on the host: %v", r.api.vaults)
	}
	stateFile, _ := os.ReadFile(filepath.Join(r.dir, ".solaria", "state", "ma-cloud.json"))
	for _, secret := range []string{"docs-secret", "gh-secret"} {
		if strings.Contains(r.golden.String(), secret) || strings.Contains(string(stateFile), secret) {
			t.Errorf("a %s value reached the transcript or the state", secret)
		}
	}
	r.check("vault")
}
