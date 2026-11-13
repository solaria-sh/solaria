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
	"github.com/solaria-sh/solaria/internal/resolve"
)

func newDigestCommand() *cobra.Command {
	var (
		env    string
		agents []string
		output string
	)
	cmd := &cobra.Command{
		Use:   "digest [-t <target>] [-a <agent>]... [path...]",
		Short: "Print each agent's digests",
		Long: `Digest prints the digests that identify each agent. The specDigest identifies
the agent as written and is the same in every target. With -t, digest
also prints the resolvedDigest: the agent after that target's
overrides, which is what plan compares with state.

Every agent is printed unless -a names some. Digest reads only the
configuration: no state, no host.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("--output must be text or json, not %q", output)
			}
			set, err := loadProject(cmd, args)
			if err != nil {
				return err
			}
			res, err := digests(set, env, agents)
			if err != nil {
				return err
			}
			if output == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			return writeDigests(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVarP(&env, "target", "t", "", "also print resolved digests for this target")
	cmd.Flags().StringArrayVarP(&agents, "agent", "a", nil, "print only this agent (repeatable)")
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text or json")
	return cmd
}

// digestResult is the JSON output of solaria digest. Its shape is part of
// the CLI's interface.
type digestResult struct {
	DefinitionFormat string        `json:"definitionFormat"`
	Target           string        `json:"target,omitempty"`
	Agents           []agentDigest `json:"agents"`
}

type agentDigest struct {
	LogicalID      string `json:"logicalId"`
	SpecDigest     string `json:"specDigest"`
	ResolvedDigest string `json:"resolvedDigest,omitempty"`
}

func digests(set *config.Set, env string, names []string) (*digestResult, error) {
	for _, n := range names {
		if set.Lookup(v1alpha1.KindAgent, n) == nil {
			return nil, fmt.Errorf("no Agent named %q", n)
		}
	}
	want := func(name string) bool { return len(names) == 0 || slices.Contains(names, name) }
	res := &digestResult{DefinitionFormat: resolve.Format, Target: env, Agents: []agentDigest{}}

	if env != "" {
		resolved, err := resolve.Target(set, env)
		if err != nil {
			return nil, err
		}
		got := map[string]bool{}
		for _, a := range resolved {
			got[a.Name] = true
			if want(a.Name) {
				res.Agents = append(res.Agents, agentDigest{LogicalID: a.ID(), SpecDigest: a.SpecDigest, ResolvedDigest: a.ResolvedDigest})
			}
		}
		for _, n := range names {
			if !got[n] {
				return nil, fmt.Errorf("agent %q isn't selected by Target %q; its agents.selector leaves it out", n, env)
			}
		}
		return res, nil
	}

	for _, r := range set.Kind(v1alpha1.KindAgent) {
		if !want(r.Name) {
			continue
		}
		d, err := resolve.SpecDigest(set, r.Name)
		if err != nil {
			return nil, err
		}
		res.Agents = append(res.Agents, agentDigest{LogicalID: r.ID(), SpecDigest: d})
	}
	slices.SortFunc(res.Agents, func(a, b agentDigest) int { return strings.Compare(a.LogicalID, b.LogicalID) })
	return res, nil
}

func writeDigests(w io.Writer, res *digestResult) error {
	var b strings.Builder
	// Writes to a strings.Builder can't fail.
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	if res.Target == "" {
		_, _ = fmt.Fprintln(tw, "AGENT\tSPEC DIGEST")
	} else {
		_, _ = fmt.Fprintln(tw, "AGENT\tSPEC DIGEST\tRESOLVED DIGEST")
	}
	for _, a := range res.Agents {
		if res.Target == "" {
			_, _ = fmt.Fprintf(tw, "%s\t%s\n", a.LogicalID, a.SpecDigest)
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", a.LogicalID, a.SpecDigest, a.ResolvedDigest)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}
