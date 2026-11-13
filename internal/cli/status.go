package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/lockfile"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/providers"
	"github.com/solaria-sh/solaria/internal/providers/container"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

func newStatusCommand() *cobra.Command {
	var (
		f      projectFlags
		agent  string
		output string
	)
	cmd := &cobra.Command{
		Use:   "status [-t <target> [-a <agent>]] [path...]",
		Short: "Show what is deployed, and whether it matches the configuration",
		Long: `Status reads the state of one target, or of every target that has
state, and compares each agent's recorded digest with the configuration. It
doesn't contact any host; run plan to check for drift.

With -a, it shows one agent and how to start it: the command, how it takes
the task, and the variables it needs. -o json gives the same in a documented
shape, so scripts don't depend on the state format.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("--output must be text or json, not %q", output)
			}
			if agent != "" && f.env == "" {
				return fmt.Errorf("--agent needs --target: an agent's status is per target")
			}
			set, err := loadProject(cmd, args)
			if err != nil {
				return err
			}
			lock, err := lockfile.Read(f.lockFile)
			if err != nil {
				return err
			}
			envs := []string{f.env}
			if f.env == "" {
				if envs, err = targetsWithState(set, f.stateDir); err != nil {
					return err
				}
			}
			res := statusResult{Targets: []envStatus{}}
			for _, env := range envs {
				backend, err := stateBackend(set, env, f.stateDir)
				if err != nil {
					return err
				}
				st, err := backend.Read(env)
				if err != nil {
					return err
				}
				if st == nil {
					st = state.New(env)
				}
				es, err := collectStatus(set, st, lock)
				if err != nil {
					return err
				}
				if agent != "" {
					if es, err = es.only(agent); err != nil {
						return err
					}
				}
				res.Targets = append(res.Targets, es)
			}
			w := cmd.OutOrStdout()
			if output == "json" {
				return encodeJSON(w, res)
			}
			if len(envs) == 0 {
				_, err := fmt.Fprintln(w, "No target has state yet. Run solaria apply -t <target> first.")
				return err
			}
			for i, es := range res.Targets {
				if i > 0 {
					if _, err := fmt.Fprintln(w); err != nil {
						return err
					}
				}
				if err := writeStatus(w, es, f.lockFile, agent != ""); err != nil {
					return err
				}
			}
			return nil
		},
	}
	f.register(cmd, false)
	registerLockFile(cmd, &f.lockFile)
	cmd.Flags().StringVarP(&agent, "agent", "a", "", "show one agent of the target, with how to start it")
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text or json")
	return cmd
}

// statusResult is what status -o json prints. Its shape is documented in
// docs/concepts.md; change it only together with that page.
type statusResult struct {
	Targets []envStatus `json:"targets"`
}

type envStatus struct {
	Name string `json:"name"`
	// Serial is the state's serial; 0 when the target has no state.
	Serial int `json:"serial"`
	// InConfiguration is false for a target that only has state.
	InConfiguration bool `json:"inConfiguration"`
	// Locked means solaria.lock pins the target.
	Locked bool        `json:"locked"`
	Host   *hostStatus `json:"host,omitempty"`
	// Vaults are host vaults, for providers that keep them (ADR 0016).
	Vaults []hostStatus `json:"vaults,omitempty"`
	Agents []agentState `json:"agents"`
}

type hostStatus struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Placement string    `json:"placement"`
	HostID    string    `json:"hostId"`
	Version   string    `json:"version,omitempty"`
	Digest    string    `json:"digest"`
	AppliedAt time.Time `json:"appliedAt"`
	// UsedBy lists the agents whose sessions attach a vault.
	UsedBy []string `json:"usedBy,omitempty"`
}

// vaultRef is a host vault an agent's sessions attach.
type vaultRef struct {
	Name   string `json:"name"`
	HostID string `json:"hostId"`
}

type agentState struct {
	LogicalID string `json:"logicalId"`
	// Annotations are the agent's metadata.annotations, read from the
	// configuration: they're never applied, so state doesn't hold them.
	Annotations map[string]string `json:"annotations,omitempty"`
	Provider    string            `json:"provider,omitempty"`
	Placement   string            `json:"placement,omitempty"`
	HostID      string            `json:"hostId,omitempty"`
	Version     string            `json:"version,omitempty"`
	// Runtime is what runs the agent loop, at the version applied.
	Runtime        string     `json:"runtime,omitempty"`
	SpecDigest     string     `json:"specDigest,omitempty"`
	ResolvedDigest string     `json:"resolvedDigest,omitempty"`
	AppliedAt      *time.Time `json:"appliedAt,omitempty"`
	// Configuration compares what was applied with the configuration.
	Configuration configStatus `json:"configuration"`
	// LockedDigest is the resolvedDigest solaria.lock pins the agent to.
	LockedDigest string `json:"lockedDigest,omitempty"`
	// Run is the command that starts the agent, ready for a shell.
	Run        string            `json:"run,omitempty"`
	Invocation *state.Invocation `json:"invocation,omitempty"`
	// Vaults are the host vaults to attach when starting a session, on
	// hosts that keep credentials in vaults.
	Vaults []vaultRef `json:"vaults,omitempty"`
	// Repositories are the agent's repositories (ADR 0017), read from the
	// configuration: where each comes from belongs to the target, and
	// on hosts the caller attaches them to, nothing is applied.
	Repositories []repositoryStatus `json:"repositories,omitempty"`
}

// repositoryStatus is a repository an agent works on, and how it reaches
// the run.
type repositoryStatus struct {
	Name   string `json:"name"`
	Access string `json:"access"`
	URL    string `json:"url"`
	Ref    string `json:"ref,omitempty"`
	Depth  int    `json:"depth,omitempty"`
	// Secret names the secret holding the token that authenticates the
	// clone.
	Secret string `json:"secret,omitempty"`
	// Path is where the run finds it: relative to the workspace when the
	// command clones it, or the mount path to attach it at.
	Path string `json:"path"`
	// Attach is the session resource to attach it as, on hosts that clone
	// it themselves: github_repository. Absent when the command clones it.
	Attach string `json:"attach,omitempty"`
}

// githubRepository is the Managed Agents session resource for a GitHub
// repository.
const githubRepository = "github_repository"

// repositories describes an agent's repositories as its provider gets
// them.
func repositories(d *resolve.Agent) []repositoryStatus {
	repos := d.Repositories()
	if len(repos) == 0 {
		return nil
	}
	attach := false
	if p, err := providers.New(d.Provider, provider.Options{Target: d.Target}); err == nil {
		attach = p.Capabilities(d.Placement).Repositories == provider.RepositoriesGitHub
	}
	out := make([]repositoryStatus, len(repos))
	for i, r := range repos {
		out[i] = repositoryStatus{
			Name: r.Name, Access: string(r.Access), URL: r.URL, Ref: r.Ref, Depth: r.Depth,
			Secret: r.SecretRef, Path: "repositories/" + r.Name,
		}
		if attach {
			out[i].Attach, out[i].Path = githubRepository, "/workspace/repositories/"+r.Name
		}
	}
	return out
}

// configStatus compares an agent's state with the configuration.
type configStatus string

const (
	configUpToDate         configStatus = "upToDate"
	configChanged          configStatus = "changed"
	configOverridesChanged configStatus = "overridesChanged"
	configRemoved          configStatus = "removed"
	configNotApplied       configStatus = "notApplied"
	configNoLongerSelected configStatus = "noLongerSelected"
)

// text is how the status table says it.
func (c configStatus) text() string {
	switch c {
	case configUpToDate:
		return "up to date"
	case configOverridesChanged:
		return "overrides changed"
	case configRemoved:
		return "removed from configuration"
	case configNotApplied:
		return "not applied"
	case configNoLongerSelected:
		return "no longer selected"
	default:
		return string(c)
	}
}

// collectStatus compares a target's state with the configuration
// and the lock file.
func collectStatus(set *config.Set, st *state.State, lock *lockfile.File) (envStatus, error) {
	es := envStatus{
		Name:            st.Target,
		Serial:          st.Serial,
		InConfiguration: set.Lookup(v1alpha1.KindTarget, st.Target) != nil,
		Locked:          lock.Locked(st.Target),
		Agents:          []agentState{},
	}
	desired := map[string]*resolve.Agent{}
	if es.InConfiguration {
		agents, err := resolve.Target(set, st.Target)
		if err != nil {
			return es, err
		}
		for i := range agents {
			desired[agents[i].ID()] = &agents[i]
		}
	}
	var pins map[string]lockfile.Entry
	if es.Locked {
		pins = lock.Targets[st.Target].Agents
		for id := range pins {
			if st.Agents[id] == nil && desired[id] == nil {
				desired[id] = nil // listed as locked but not applied
			}
		}
	}
	if h := st.Host; h != nil {
		es.Host = &hostStatus{
			ID: h.ID, Provider: h.Provider, Placement: h.Placement, HostID: h.Binding.HostID,
			Version: h.Binding.Version, Digest: h.Digest, AppliedAt: h.AppliedAt,
		}
	}

	for _, id := range st.VaultIDs() {
		v := st.Vaults[id]
		vs := hostStatus{
			ID: v.ID, Provider: v.Provider, Placement: v.Placement, HostID: v.Binding.HostID,
			Version: v.Binding.Version, Digest: v.Digest, AppliedAt: v.AppliedAt,
		}
		for _, aid := range st.IDs() {
			if _, ok := st.Agents[aid].Definition.Vaults[strings.TrimPrefix(id, v1alpha1.KindVault+"/")]; ok {
				vs.UsedBy = append(vs.UsedBy, aid)
			}
		}
		es.Vaults = append(es.Vaults, vs)
	}

	ids := st.IDs()
	for id := range desired {
		if st.Agents[id] == nil {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		as := agentState{LogicalID: id}
		if r := set.Lookup(v1alpha1.KindAgent, strings.TrimPrefix(id, v1alpha1.KindAgent+"/")); r != nil {
			as.Annotations = r.Agent().Metadata.Annotations
		}
		if p, ok := pins[id]; ok {
			as.LockedDigest = p.ResolvedDigest
		}
		e, d := st.Agents[id], desired[id]
		if e == nil {
			as.Configuration = configNotApplied
			if d == nil {
				as.Configuration = configNoLongerSelected
			}
			es.Agents = append(es.Agents, as)
			continue
		}
		applied := e.AppliedAt
		as.Provider, as.Placement = e.Provider, e.Placement
		as.HostID, as.Version, as.Runtime = e.Binding.HostID, e.Binding.Version, e.Binding.Runtime
		as.SpecDigest, as.ResolvedDigest = e.SpecDigest, e.ResolvedDigest
		as.AppliedAt = &applied
		as.Configuration = compareConfig(e, d)
		as.Run = e.Binding.Outputs["run"]
		as.Invocation = e.Binding.Invocation
		for _, name := range slices.Sorted(maps.Keys(e.Definition.Vaults)) {
			if v := st.Vaults[v1alpha1.KindVault+"/"+name]; v != nil {
				as.Vaults = append(as.Vaults, vaultRef{Name: name, HostID: v.Binding.HostID})
			}
		}
		if d != nil {
			as.Repositories = repositories(d)
		}
		es.Agents = append(es.Agents, as)
	}
	return es, nil
}

// only narrows the status to one agent, by name. The agent must be in the
// state or in the configuration.
func (es envStatus) only(name string) (envStatus, error) {
	id := v1alpha1.KindAgent + "/" + name
	for _, as := range es.Agents {
		if as.LogicalID == id {
			es.Host, es.Vaults = nil, nil
			es.Agents = []agentState{as}
			return es, nil
		}
	}
	return es, fmt.Errorf("agent %q isn't in the state of Target %q, and its configuration doesn't select it", name, es.Name)
}

func compareConfig(e *state.Entry, d *resolve.Agent) configStatus {
	switch {
	case d == nil:
		return configRemoved
	case d.ResolvedDigest == e.ResolvedDigest:
		return configUpToDate
	case d.SpecDigest == e.SpecDigest:
		return configOverridesChanged
	default:
		return configChanged
	}
}

// writeStatus prints a target's status as a table. With detail, it
// also prints how to start each agent.
func writeStatus(w io.Writer, es envStatus, lockPath string, detail bool) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Target %s (state serial %d", es.Name, es.Serial)
	if es.Locked {
		fmt.Fprintf(&b, ", locked in %s", lockPath)
	}
	b.WriteString(")\n")
	if !es.InConfiguration {
		b.WriteString("  not in the configuration; solaria destroy -t " + es.Name + " removes what is left\n")
	}
	// Writes to a strings.Builder can't fail.
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	// lockCol is the LOCKED column, present only for a locked target.
	lockCol := func(digest string) string {
		if !es.Locked {
			return ""
		}
		if digest != "" {
			return "\t" + provider.ShortDigest(digest)
		}
		return "\t-"
	}
	header := "  OBJECT\tPROVIDER\tHOST ID\tVERSION\tDIGEST\tAPPLIED\tCONFIGURATION"
	if es.Locked {
		header += "\tLOCKED"
	}
	_, _ = fmt.Fprintln(tw, header)
	if h := es.Host; h != nil {
		_, _ = fmt.Fprintf(tw, "  %s\t%s/%s\t%s\t%s\t%s\t%s\t%s%s\n", h.ID, h.Provider, h.Placement, h.HostID,
			orDash(h.Version), provider.ShortDigest(h.Digest), h.AppliedAt.Format("2006-01-02 15:04"), "shared by the agents", lockCol(""))
	}
	for _, v := range es.Vaults {
		used := "unused"
		if len(v.UsedBy) > 0 {
			used = "for " + strings.Join(v.UsedBy, ", ")
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s/%s\t%s\t%s\t%s\t%s\t%s%s\n", v.ID, v.Provider, v.Placement, v.HostID,
			orDash(v.Version), provider.ShortDigest(v.Digest), v.AppliedAt.Format("2006-01-02 15:04"), used, lockCol(""))
	}
	for _, as := range es.Agents {
		if as.AppliedAt == nil {
			_, _ = fmt.Fprintf(tw, "  %s\t-\t-\t-\t-\t-\t%s%s\n", as.LogicalID, as.Configuration.text(), lockCol(as.LockedDigest))
			continue
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s/%s\t%s\t%s\t%s\t%s\t%s%s\n", as.LogicalID, as.Provider, as.Placement, as.HostID,
			orDash(as.Version), provider.ShortDigest(as.ResolvedDigest), as.AppliedAt.Format("2006-01-02 15:04"),
			as.Configuration.text(), lockCol(as.LockedDigest))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if detail {
		for _, as := range es.Agents {
			b.WriteString("\n")
			writeInvocation(&b, es.Name, as)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeInvocation says what runs an agent and how to start it.
func writeInvocation(b *strings.Builder, env string, as agentState) {
	inv := as.Invocation
	if as.AppliedAt == nil {
		fmt.Fprintf(b, "  Not applied; run solaria apply -t %s first.\n", env)
		return
	}
	if as.Runtime != "" {
		fmt.Fprintf(b, "  runtime: %s\n", as.Runtime)
	}
	if as.Run == "" && inv == nil {
		fmt.Fprintf(b, "  %s runs the agent on its host; there's no command to start it.\n", as.Provider)
		if len(as.Vaults) > 0 {
			refs := make([]string, len(as.Vaults))
			for i, v := range as.Vaults {
				refs[i] = fmt.Sprintf("%s (%s)", v.HostID, v.Name)
			}
			fmt.Fprintf(b, "  vaults: attach %s when starting a session\n", strings.Join(refs, ", "))
		}
		writeRepositories(b, as.Repositories)
		return
	}
	run := as.Run
	if run == "" {
		run = container.ShellCommand(inv.Argv)
	}
	fmt.Fprintf(b, "  run: %s\n", run)
	if inv == nil {
		b.WriteString("  Applied before Solaria recorded what the command needs; apply again to see it.\n")
		return
	}
	var task []string
	if inv.Task.Flag != "" {
		task = append(task, "after the command with "+inv.Task.Flag)
	}
	if inv.Task.Stdin {
		task = append(task, "on stdin")
	}
	if len(task) > 0 {
		fmt.Fprintf(b, "  task: %s\n", strings.Join(task, ", or "))
	}
	ws := ""
	switch inv.Workspace {
	case state.WorkspaceCwd:
		ws = "the directory it runs in"
	case state.WorkspaceMount:
		ws = "the directory it runs in, mounted into the container"
	case state.WorkspaceBound:
		ws = inv.WorkspacePath + ", wherever it runs"
	}
	if ws != "" {
		if inv.WorkspaceAccess == string(v1alpha1.AccessReadOnly) {
			ws += " (read-only)"
		}
		fmt.Fprintf(b, "  workspace: %s\n", ws)
	}
	if len(inv.Env) > 0 {
		fmt.Fprintf(b, "  environment: %s\n", strings.Join(inv.Env, ", "))
	}
	if inv.EnvFile != "" {
		fmt.Fprintf(b, "  secrets from: %s\n", inv.EnvFile)
	}
	if inv.Result != "" {
		fmt.Fprintf(b, "  prints: %s\n", inv.Result)
	}
	writeRepositories(b, as.Repositories)
}

// writeRepositories says, for each repository, where it comes from and
// where the run finds it.
func writeRepositories(b *strings.Builder, repos []repositoryStatus) {
	for _, r := range repos {
		from := r.URL
		if r.Ref != "" {
			from += " at " + r.Ref
		}
		var token string
		if r.Secret != "" {
			token = ", with the token in " + r.Secret
		}
		if r.Attach != "" {
			fmt.Fprintf(b, "  repository %s: attach %s as a %s resource at %s when starting a session%s (%s)\n", r.Name, from, r.Attach, r.Path, token, r.Access)
			continue
		}
		fmt.Fprintf(b, "  repository %s: cloned from %s into %s%s (%s)\n", r.Name, from, r.Path, token, r.Access)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
