// Command solaria-worker serves a Managed Agents self-hosted environment:
// it polls the environment's work queue and runs the agents' tools in this
// container, with the SDK's EnvironmentWorker. The managed-agents provider
// builds it into the worker image and runs one container per environment.
//
// It reads ANTHROPIC_ENVIRONMENT_ID and ANTHROPIC_ENVIRONMENT_KEY. The key
// only reaches this environment's queue; the container never holds an API
// key, since agent tool calls run here.
//
// The agents' stdio MCP servers run here too (ADR 0021). The image installs
// them and describes them in /etc/solaria/worker.json. The worker serves each
// server's tools as custom tools named <server>__<tool>, which the agents
// declare: it starts a session's server when the session first calls one
// of its tools, in the session's workdir, and stops it when the session
// ends. "solaria-worker list-tools" starts each server once and prints its
// tools as JSON, which apply declares on the agents.
//
// worker.json also lists the Environment's setup steps (ADR 0022). The
// worker runs them at the start of each session, before it serves the
// session's tools, in the shared workdir. When one fails, every tool call
// of that session returns the failure, so the agent can't work on a
// workspace that wasn't prepared.
//
// The image builds it as module solaria-worker, from go.mod.txt and
// go.sum.txt, which pin the same anthropic-sdk-go and MCP go-sdk versions
// as Solaria's go.mod. After either changes there, or after this file
// imports a new module, run "make worker-mod" to regenerate them.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/lib/environments"
	"github.com/anthropics/anthropic-sdk-go/v2/mcp"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/tools/agenttoolset"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// configPath is where the image describes the MCP servers it installed.
const configPath = "/etc/solaria/worker.json"

// Timeouts for MCP servers: starting one and listing its tools, and one
// tool call, so a hung server fails the call instead of the session.
const (
	startTimeout = 2 * time.Minute
	callTimeout  = 10 * time.Minute
)

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "list-tools" {
		err = listTools(os.Stdout)
	} else {
		err = run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "solaria-worker:", err)
		os.Exit(1)
	}
}

func run() error {
	id, key := os.Getenv("ANTHROPIC_ENVIRONMENT_ID"), os.Getenv("ANTHROPIC_ENVIRONMENT_KEY")
	if id == "" || key == "" {
		return errors.New("ANTHROPIC_ENVIRONMENT_ID and ANTHROPIC_ENVIRONMENT_KEY must be set")
	}
	// Cancelling, rather than killing, lets the worker finish the work item
	// it holds and upload changed memory files.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	// The tools are declared on the agents, so the worker lists them once,
	// at startup, to know their names.
	listed := map[string][]*mcpsdk.Tool{}
	for _, s := range cfg.Servers {
		tools, err := s.list(ctx, "/workspace")
		if err != nil {
			return err
		}
		listed[s.Name] = tools
	}

	client := anthropic.NewClient(option.WithAuthToken(key))
	worker := environments.NewEnvironmentWorker(client, environments.EnvironmentWorkerOptions{
		EnvironmentID:  id,
		EnvironmentKey: key,
		Workdir:        "/workspace",
		ToolsFunc: func(env *agenttoolset.AgentToolContext) []anthropic.BetaTool {
			// Without Env, bash inherits this container's environment minus
			// only ANTHROPIC_*, which would hand the agent the secrets of
			// the servers below.
			env.Env = bashEnv(os.Environ(), cfg.secretVars())
			tools := agenttoolset.BetaAgentToolset20260401(env)
			sessions := &sessionServers{workdir: env.Workdir}
			for _, s := range cfg.Servers {
				for _, t := range listed[s.Name] {
					tools = append(tools, newWrappedTool(sessions, s, t))
				}
			}
			if err := runSetup(ctx, cfg.Setup, env.Workdir, os.Stderr); err != nil {
				fmt.Fprintln(os.Stderr, "solaria-worker:", err)
				return failAll(tools, err)
			}
			return tools
		},
	})
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// listedTool is one tool as list-tools prints it.
type listedTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// listTools starts each server, lists its tools, and prints them as JSON,
// keyed by server.
func listTools(w *os.File) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	out := map[string][]listedTool{}
	for _, s := range cfg.Servers {
		tools, err := s.list(context.Background(), "/workspace")
		if err != nil {
			return err
		}
		list := []listedTool{}
		for _, t := range tools {
			list = append(list, listedTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
		out[s.Name] = list
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// config is /etc/solaria/worker.json.
type config struct {
	Servers []server `json:"servers"`
	// Setup is the Environment's setup steps, in order.
	Setup []step `json:"setup,omitempty"`
}

// step is a setup step (ADR 0022).
type step struct {
	Name string `json:"name"`
	// Run is the script, run with sh -e in the workdir.
	Run string `json:"run"`
	// Env holds the step's plain variables.
	Env map[string]string `json:"env,omitempty"`
	// SecretEnv maps the step's secret variables to the container
	// variables that hold their values.
	SecretEnv map[string]string `json:"secretEnv,omitempty"`
}

// runSetup runs the steps in order, each with sh -e in workdir. A step
// gets PATH, HOME and its own variables, never the rest of the worker's
// environment, which holds the environment's key and the MCP servers'
// secrets. Its output goes to out, the container's log.
func runSetup(ctx context.Context, steps []step, workdir string, out *os.File) error {
	for _, st := range steps {
		cmd := exec.CommandContext(ctx, "sh", "-e", "-c", st.Run)
		cmd.Dir = workdir
		cmd.Stdout, cmd.Stderr = out, out
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
		for _, k := range sortedKeys(st.Env) {
			cmd.Env = append(cmd.Env, k+"="+st.Env[k])
		}
		for _, k := range sortedKeys(st.SecretEnv) {
			cmd.Env = append(cmd.Env, k+"="+os.Getenv(st.SecretEnv[k]))
		}
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return fmt.Errorf("setup step %s exited with %d", st.Name, exit.ExitCode())
			}
			return fmt.Errorf("setup step %s: %w", st.Name, err)
		}
	}
	return nil
}

// failAll wraps a session's tools so that each call returns err: the
// session's setup failed, and the agent mustn't work on the workspace.
func failAll(tools []anthropic.BetaTool, err error) []anthropic.BetaTool {
	out := make([]anthropic.BetaTool, len(tools))
	for i, t := range tools {
		out[i] = failedTool{BetaTool: t, err: err}
	}
	return out
}

// failedTool is a tool whose session's setup failed.
type failedTool struct {
	anthropic.BetaTool
	err error
}

func (t failedTool) Execute(context.Context, json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	return nil, fmt.Errorf("the session's workspace isn't ready: %w", t.err)
}

// Close closes the tool it wraps, as the worker does with the tools it's
// given when the session ends.
func (t failedTool) Close() error {
	if c, ok := t.BetaTool.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

// server is an MCP server the image installed.
type server struct {
	Name string `json:"name"`
	// Runner is npm or pypi.
	Runner string `json:"runner"`
	// Dir is where the image installed it: an npm prefix or a virtual
	// environment.
	Dir string `json:"dir"`
	// Package is the npm or PyPI package.
	Package string   `json:"package"`
	Args    []string `json:"args,omitempty"`
	// Env holds the server's plain variables.
	Env map[string]string `json:"env,omitempty"`
	// SecretEnv maps the server's secret variables to the container
	// variables that hold their values.
	SecretEnv map[string]string `json:"secretEnv,omitempty"`
}

// secretVars lists the container variables that hold the servers' and
// the setup steps' secrets.
func (c *config) secretVars() []string {
	var out []string
	for _, s := range c.Servers {
		for _, v := range s.SecretEnv {
			out = append(out, v)
		}
	}
	for _, st := range c.Setup {
		for _, v := range st.SecretEnv {
			out = append(out, v)
		}
	}
	return out
}

// bashEnv is the environment the agent's bash gets: the container's,
// without the worker's own credentials (ANTHROPIC_*) or the variables that
// hold the MCP servers' and setup steps' secrets, which only they get.
func bashEnv(environ, secretVars []string) map[string]string {
	out := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.HasPrefix(k, "ANTHROPIC_") || slices.Contains(secretVars, k) {
			continue
		}
		out[k] = v
	}
	return out
}

func loadConfig(path string) (*config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// command is how to start the server in workdir. It gets only its own
// variables, never the worker's: those include the environment's key.
func (s server) command(workdir string) (*exec.Cmd, error) {
	var bin string
	switch s.Runner {
	case "npm":
		name, err := npmBin(s.Dir, s.Package)
		if err != nil {
			return nil, fmt.Errorf("MCP server %s: %w", s.Name, err)
		}
		bin = filepath.Join(s.Dir, "node_modules", ".bin", name)
	case "pypi":
		// uvx's convention: the command is named after the package.
		bin = filepath.Join(s.Dir, "bin", s.Package)
	default:
		return nil, fmt.Errorf("MCP server %s: unknown runner %q", s.Name, s.Runner)
	}
	cmd := exec.Command(bin, s.Args...)
	cmd.Dir = workdir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "LANG=C.UTF-8"}
	for _, k := range sortedKeys(s.Env) {
		cmd.Env = append(cmd.Env, k+"="+s.Env[k])
	}
	for _, k := range sortedKeys(s.SecretEnv) {
		if v, ok := os.LookupEnv(s.SecretEnv[k]); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Stderr = os.Stderr
	return cmd, nil
}

// npmBin finds the command an npm package installs, as npx does: its only
// bin, or the one named after the package without its scope.
func npmBin(dir, pkg string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "node_modules", pkg, "package.json"))
	if err != nil {
		return "", err
	}
	var m struct {
		Name string          `json:"name"`
		Bin  json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("%s's package.json: %w", pkg, err)
	}
	unscoped := m.Name[strings.LastIndex(m.Name, "/")+1:]
	var one string
	if json.Unmarshal(m.Bin, &one) == nil && one != "" {
		return unscoped, nil
	}
	var bins map[string]string
	if err := json.Unmarshal(m.Bin, &bins); err != nil || len(bins) == 0 {
		return "", fmt.Errorf("package %s installs no command", pkg)
	}
	if len(bins) == 1 {
		for name := range bins {
			return name, nil
		}
	}
	if _, ok := bins[unscoped]; ok {
		return unscoped, nil
	}
	return "", fmt.Errorf("package %s installs %d commands and none is named %s", pkg, len(bins), unscoped)
}

// connect starts the server and opens an MCP session with it.
func (s server) connect(ctx context.Context, workdir string) (*mcpsdk.ClientSession, error) {
	cmd, err := s.command(workdir)
	if err != nil {
		return nil, err
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "solaria-worker", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	session, err := client.Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("start MCP server %s: %w", s.Name, err)
	}
	return session, nil
}

// list starts the server, lists its tools and stops it.
func (s server) list(ctx context.Context, workdir string) ([]*mcpsdk.Tool, error) {
	session, err := s.connect(ctx, workdir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	var tools []*mcpsdk.Tool
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("list the tools of MCP server %s: %w", s.Name, err)
		}
		tools = append(tools, t)
	}
	slices.SortFunc(tools, func(a, b *mcpsdk.Tool) int { return strings.Compare(a.Name, b.Name) })
	return tools, nil
}

// sessionServers are one session's running MCP servers, started on first
// use and stopped when the session ends.
type sessionServers struct {
	workdir string
	mu      sync.Mutex
	running map[string]*mcpsdk.ClientSession
	closed  bool
}

func (ss *sessionServers) session(ctx context.Context, s server) (*mcpsdk.ClientSession, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return nil, errors.New("the session has ended")
	}
	if cs, ok := ss.running[s.Name]; ok {
		return cs, nil
	}
	cs, err := s.connect(ctx, ss.workdir)
	if err != nil {
		return nil, err
	}
	if ss.running == nil {
		ss.running = map[string]*mcpsdk.ClientSession{}
	}
	ss.running[s.Name] = cs
	return cs, nil
}

// close stops the servers, once for all of the session's tools.
func (ss *sessionServers) close() error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return nil
	}
	ss.closed = true
	var errs []error
	for _, cs := range ss.running {
		errs = append(errs, cs.Close())
	}
	return errors.Join(errs...)
}

// wrappedTool serves one MCP tool as a custom tool named <server>__<tool>.
type wrappedTool struct {
	sessions *sessionServers
	server   server
	tool     *mcpsdk.Tool
	schema   anthropic.BetaToolInputSchemaParam
}

func newWrappedTool(ss *sessionServers, s server, t *mcpsdk.Tool) *wrappedTool {
	w := &wrappedTool{sessions: ss, server: s, tool: t}
	// The session is only used to call the tool; the schema needs none.
	if bt, err := mcp.NewBetaTool(t, nil); err == nil {
		w.schema = bt.InputSchema()
	}
	return w
}

func (w *wrappedTool) Name() string { return w.server.Name + "__" + w.tool.Name }

func (w *wrappedTool) Description() string { return w.tool.Description }

func (w *wrappedTool) InputSchema() anthropic.BetaToolInputSchemaParam { return w.schema }

func (w *wrappedTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	cs, err := w.sessions.session(ctx, w.server)
	if err != nil {
		return nil, err
	}
	bt, err := mcp.NewBetaTool(w.tool, cs)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return bt.Execute(ctx, input)
}

// Close stops the session's servers; the worker closes every tool when
// the session ends.
func (w *wrappedTool) Close() error {
	return w.sessions.close()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
