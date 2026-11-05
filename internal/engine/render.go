package engine

import (
	"fmt"
	"io"
	"strings"

	"github.com/renantarouco/solaria/internal/provider"
)

// symbols mark each action in a rendered plan, Terraform style.
var symbols = map[provider.Action]string{
	provider.Create:  "+",
	provider.Update:  "~",
	provider.Replace: "-/+",
	provider.Delete:  "-",
}

// Render writes the plan for people to read. Diagnostics aren't part of
// it; callers print them separately.
func Render(w io.Writer, plan *Plan) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Environment %s (%s/%s)\n", plan.Environment, plan.Provider, plan.Placement)
	if !plan.HasChanges() {
		b.WriteString("\nNo changes. The environment matches the configuration.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}
	// Steps show in the order Apply takes them: the host object comes
	// before the agents, except a delete, which comes after.
	h := plan.Host
	if h != nil && h.Action != provider.NoOp && h.Action != provider.Delete {
		renderHost(&b, h)
	}
	for _, s := range plan.Steps {
		if s.Action == provider.NoOp {
			continue
		}
		renderStep(&b, plan, s)
	}
	if h != nil && h.Action == provider.Delete {
		renderHost(&b, h)
	}
	c, u, r, d := plan.Counts()
	fmt.Fprintf(&b, "\nPlan: %d to create, %d to update, %d to replace, %d to %s.\n", c, u, r, d, deleteVerb(plan))
	_, err := io.WriteString(w, b.String())
	return err
}

func renderStep(b *strings.Builder, plan *Plan, s *provider.Plan) {
	sym := symbols[s.Action]
	if s.Action == provider.Delete && plan.Capabilities.Destroy == provider.DestroyArchive && s.Prior.Provider == plan.Provider {
		sym = "-/archive"
	}
	fmt.Fprintf(b, "\n  %s %s", sym, s.ID)
	if s.Reason != "" {
		fmt.Fprintf(b, " (%s)", s.Reason)
	}
	b.WriteString("\n")

	switch s.Action {
	case provider.Create:
		fmt.Fprintf(b, "      resolvedDigest: %s\n", provider.ShortDigest(s.Desired.ResolvedDigest))
	case provider.Delete:
		fmt.Fprintf(b, "      host: %s", s.Prior.Binding.HostID)
		if v := s.Prior.Binding.Version; v != "" {
			fmt.Fprintf(b, " version %s", v)
		}
		b.WriteString("\n")
	case provider.Update, provider.Replace:
		renderChanges(b, s.Changes)
		old, cur := s.Prior.ResolvedDigest, s.Desired.ResolvedDigest
		if old != cur {
			fmt.Fprintf(b, "      resolvedDigest: %s -> %s", provider.ShortDigest(old), provider.ShortDigest(cur))
			if s.Prior.SpecDigest == s.Desired.SpecDigest {
				b.WriteString(" (specDigest unchanged: only this environment's overrides differ)")
			}
			b.WriteString("\n")
		}
	}
	for _, n := range s.Notes {
		fmt.Fprintf(b, "      note: %s\n", n)
	}
}

// renderHost writes the step for the environment's own host object.
func renderHost(b *strings.Builder, h *provider.EnvironmentPlan) {
	fmt.Fprintf(b, "\n  %s %s", symbols[h.Action], h.ID)
	if h.Reason != "" {
		fmt.Fprintf(b, " (%s)", h.Reason)
	}
	b.WriteString("\n")
	switch h.Action {
	case provider.Delete:
		fmt.Fprintf(b, "      host: %s\n", h.Prior.Binding.HostID)
	case provider.Update, provider.Replace:
		renderChanges(b, h.Changes)
	}
	for _, n := range h.Notes {
		fmt.Fprintf(b, "      note: %s\n", n)
	}
}

func renderChanges(b *strings.Builder, changes []provider.Change) {
	for _, c := range changes {
		switch {
		case c.Text && c.Old == "(none)":
			fmt.Fprintf(b, "      + %s: added (%s)\n", c.Path, c.New)
		case c.Text && c.New == "(none)":
			fmt.Fprintf(b, "      - %s: removed (%s)\n", c.Path, c.Old)
		case c.Text:
			fmt.Fprintf(b, "      ~ %s: text changed (%s -> %s)\n", c.Path, c.Old, c.New)
		default:
			fmt.Fprintf(b, "      ~ %s: %s -> %s\n", c.Path, c.Old, c.New)
		}
	}
}

func deleteVerb(plan *Plan) string {
	if plan.Capabilities.Destroy == provider.DestroyArchive {
		return "delete or archive"
	}
	return "delete"
}
