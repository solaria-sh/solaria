package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/lockfile"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

func newStatusCommand() *cobra.Command {
	var (
		f      projectFlags
		output string
	)
	cmd := &cobra.Command{
		Use:   "status [-e <environment>] [path...]",
		Short: "Show what is deployed, and whether it matches the configuration",
		Long: `Status reads the state of one environment, or of every environment that has
state, and compares each agent's recorded digest with the configuration. It
doesn't contact any host; run plan to check for drift.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("--output must be text or json, not %q", output)
			}
			set, err := loadProject(cmd, args)
			if err != nil {
				return err
			}
			lock, err := lockfile.Read(f.lockFile)
			if err != nil {
				return err
			}
			backend := &state.Local{Dir: f.stateDir}
			envs := []string{f.env}
			if f.env == "" {
				if envs, err = backend.Environments(); err != nil {
					return err
				}
			}
			states := map[string]*state.State{}
			for _, env := range envs {
				st, err := backend.Read(env)
				if err != nil {
					return err
				}
				if st == nil {
					st = state.New(env)
				}
				states[env] = st
			}
			if output == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(states)
			}
			if len(envs) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No environment has state yet. Run solaria apply -e <environment> first.")
				return err
			}
			for i, env := range envs {
				if i > 0 {
					if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
						return err
					}
				}
				if err := writeStatus(cmd.OutOrStdout(), set, states[env], lock, f.lockFile); err != nil {
					return err
				}
			}
			return nil
		},
	}
	f.register(cmd, false)
	registerLockFile(cmd, &f.lockFile)
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text or json")
	return cmd
}

func writeStatus(w io.Writer, set *config.Set, st *state.State, lock *lockfile.File, lockPath string) error {
	desired := map[string]*resolve.Agent{}
	inConfig := set.Lookup(v1alpha1.KindEnvironment, st.Environment) != nil
	if inConfig {
		agents, err := resolve.Environment(set, st.Environment)
		if err != nil {
			return err
		}
		for i := range agents {
			desired[agents[i].ID()] = &agents[i]
		}
	}

	locked := lock.Locked(st.Environment)
	var pins map[string]lockfile.Entry
	if locked {
		pins = lock.Environments[st.Environment].Agents
		for id := range pins {
			if st.Agents[id] == nil && desired[id] == nil {
				desired[id] = nil // listed below as locked but not applied
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Environment %s (state serial %d", st.Environment, st.Serial)
	if locked {
		fmt.Fprintf(&b, ", locked in %s", lockPath)
	}
	b.WriteString(")\n")
	if !inConfig {
		b.WriteString("  not in the configuration; solaria destroy -e " + st.Environment + " removes what is left\n")
	}
	// Writes to a strings.Builder can't fail.
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	// lockCol is the LOCKED column, present only for a locked environment.
	lockCol := func(id string) string {
		if !locked {
			return ""
		}
		if p, ok := pins[id]; ok {
			return "\t" + provider.ShortDigest(p.ResolvedDigest)
		}
		return "\t-"
	}
	header := "  OBJECT\tPROVIDER\tHOST ID\tVERSION\tDIGEST\tAPPLIED\tCONFIGURATION"
	if locked {
		header += "\tLOCKED"
	}
	_, _ = fmt.Fprintln(tw, header)
	if h := st.Host; h != nil {
		_, _ = fmt.Fprintf(tw, "  %s\t%s/%s\t%s\t%s\t%s\t%s\t%s%s\n", h.ID, h.Provider, h.Placement, h.Binding.HostID,
			orDash(h.Binding.Version), provider.ShortDigest(h.Digest), h.AppliedAt.Format("2006-01-02 15:04"), "shared by the agents", lockCol(""))
	}
	ids := st.IDs()
	for id := range desired {
		if st.Agents[id] == nil {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		e := st.Agents[id]
		d := desired[id]
		if e == nil {
			what := "not applied"
			if d == nil {
				what = "no longer selected"
			}
			_, _ = fmt.Fprintf(tw, "  %s\t-\t-\t-\t-\t-\t%s%s\n", id, what, lockCol(id))
			continue
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s/%s\t%s\t%s\t%s\t%s\t%s%s\n", id, e.Provider, e.Placement, e.Binding.HostID,
			orDash(e.Binding.Version), provider.ShortDigest(e.ResolvedDigest), e.AppliedAt.Format("2006-01-02 15:04"), compareConfig(e, d), lockCol(id))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func compareConfig(e *state.Entry, d *resolve.Agent) string {
	switch {
	case d == nil:
		return "removed from configuration"
	case d.ResolvedDigest == e.ResolvedDigest:
		return "up to date"
	case d.SpecDigest == e.SpecDigest:
		return "overrides changed"
	default:
		return "changed"
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
