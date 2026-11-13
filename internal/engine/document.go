package engine

import (
	"fmt"
	"maps"
	"slices"

	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
	"github.com/solaria-sh/solaria/internal/version"
)

// PlanFormat names the shape of Document (ADR 0018). Fields are added
// compatibly; removing or changing one needs a new format. v2 renamed
// environment to target (ADR 0020).
const PlanFormat = "solaria.sh/plan/v2"

// Document is a plan as plan -o json prints it and plan --out saves it
// (ADR 0018). It holds digests, names and summaries, never a definition's
// text or a secret value. Its shape is documented in docs/concepts.md.
type Document struct {
	Format string `json:"format"`
	// SolariaVersion and DefinitionFormat are the binary's, which apply
	// must match.
	SolariaVersion   string `json:"solariaVersion"`
	DefinitionFormat string `json:"definitionFormat"`
	Target           string `json:"target"`
	Provider         string `json:"provider"`
	Placement        string `json:"placement"`
	// Options are the plan's flags, which apply plans with again.
	Options DocumentOptions `json:"options"`
	// State is the state the plan was made from.
	State DocumentState `json:"state"`
	// Source is the checkout the plan was made from.
	Source state.Source `json:"source"`
	// Environments are the host objects of the Environments the target's
	// agents use (ADR 0012, ADR 0020), when the provider keeps them.
	Environments []HostStep `json:"environments,omitempty"`
	// Vaults are host vaults (ADR 0016), when the provider keeps them.
	Vaults []HostStep `json:"vaults,omitempty"`
	// Agents has one step per agent in the configuration or the state,
	// sorted by ID, no-ops included.
	Agents      []AgentStep `json:"agents"`
	Summary     Summary     `json:"summary"`
	Diagnostics diag.List   `json:"diagnostics"`
}

// DocumentOptions are the flags a plan was made with.
type DocumentOptions struct {
	Refresh       bool `json:"refresh"`
	Destroy       bool `json:"destroy"`
	RotateSecrets bool `json:"rotateSecrets"`
}

// DocumentState identifies a state. Lineage is empty while the
// target has no state yet, which serial 0 also says.
type DocumentState struct {
	Lineage string `json:"lineage,omitempty"`
	Serial  int    `json:"serial"`
}

// AgentDigests identify an agent's definition (ADR 0008).
type AgentDigests struct {
	SpecDigest     string `json:"specDigest"`
	ResolvedDigest string `json:"resolvedDigest"`
}

// AgentStep is what the plan does to one agent. Before is absent for a
// create and After for a delete.
type AgentStep struct {
	ID      string          `json:"id"`
	Action  provider.Action `json:"action"`
	Before  *AgentDigests   `json:"before,omitempty"`
	After   *AgentDigests   `json:"after,omitempty"`
	Changes []FieldChange   `json:"changes,omitempty"`
	Reason  string          `json:"reason,omitempty"`
	Notes   []string        `json:"notes,omitempty"`
}

// HostDigest identifies a host object's config, as its provider computes
// it.
type HostDigest struct {
	Digest string `json:"digest"`
}

// HostStep is what the plan does to an Environment's host object or to a
// host vault.
type HostStep struct {
	ID     string          `json:"id"`
	Action provider.Action `json:"action"`
	Before *HostDigest     `json:"before,omitempty"`
	After  *HostDigest     `json:"after,omitempty"`
	// Rotate sends every credential's value again (vaults only).
	Rotate  bool          `json:"rotate,omitempty"`
	Changes []FieldChange `json:"changes,omitempty"`
	Reason  string        `json:"reason,omitempty"`
	Notes   []string      `json:"notes,omitempty"`
}

// FieldChange is one changed field, as the text plan shows it.
type FieldChange struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
	// Text means Old and New summarize long text, such as "12 lines".
	Text bool `json:"text,omitempty"`
}

// Summary counts the steps that change something, host objects included.
type Summary struct {
	Create  int `json:"create"`
	Update  int `json:"update"`
	Replace int `json:"replace"`
	Delete  int `json:"delete"`
}

// Document describes the plan for programs and for apply to check.
func (e *Engine) Document(p *Plan) *Document {
	d := &Document{
		Format:           PlanFormat,
		SolariaVersion:   version.Version,
		DefinitionFormat: resolve.Format,
		Target:           p.Target,
		Provider:         p.Provider,
		Placement:        p.Placement,
		Options:          DocumentOptions(p.Options),
		State:            DocumentState{Serial: p.state.Serial},
		Source:           e.Source,
		Agents:           []AgentStep{},
		Diagnostics:      p.Diagnostics,
	}
	if p.state.Serial > 0 {
		d.State.Lineage = p.state.Lineage
	}
	if d.Diagnostics == nil {
		d.Diagnostics = diag.List{}
	}
	for _, s := range p.Steps {
		step := AgentStep{ID: s.ID, Action: s.Action, Changes: changes(s.Changes), Reason: s.Reason, Notes: s.Notes}
		if s.Prior != nil {
			step.Before = &AgentDigests{SpecDigest: s.Prior.SpecDigest, ResolvedDigest: s.Prior.ResolvedDigest}
		}
		if s.Desired != nil && s.Action != provider.Delete {
			step.After = &AgentDigests{SpecDigest: s.Desired.SpecDigest, ResolvedDigest: s.Desired.ResolvedDigest}
		}
		d.Agents = append(d.Agents, step)
	}
	for _, h := range p.Environments {
		step := hostStep(h.ID, h.Action, h.Digest, h.Changes, h.Reason, h.Notes)
		if h.Prior != nil {
			step.Before = &HostDigest{Digest: h.Prior.Digest}
		}
		d.Environments = append(d.Environments, step)
	}
	for _, v := range p.Vaults {
		step := hostStep(v.ID, v.Action, v.Digest, v.Changes, v.Reason, v.Notes)
		step.Rotate = v.Rotate
		if v.Prior != nil {
			step.Before = &HostDigest{Digest: v.Prior.Digest}
		}
		d.Vaults = append(d.Vaults, step)
	}
	d.Summary.Create, d.Summary.Update, d.Summary.Replace, d.Summary.Delete = p.Counts()
	return d
}

func hostStep(id string, action provider.Action, digest string, ch []provider.Change, reason string, notes []string) HostStep {
	s := HostStep{ID: id, Action: action, Changes: changes(ch), Reason: reason, Notes: notes}
	if action != provider.Delete {
		s.After = &HostDigest{Digest: digest}
	}
	return s
}

func changes(in []provider.Change) []FieldChange {
	out := make([]FieldChange, 0, len(in))
	for _, c := range in {
		out = append(out, FieldChange{Path: c.Path, Old: c.Old, New: c.New, Text: c.Text})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Differences lists what a fresh plan does differently from the saved one
// (ADR 0018): the Solaria version or definition format, the state, or any
// step's action or digests. Empty means applying the fresh plan does
// exactly what the saved one showed.
func (d *Document) Differences(fresh *Document) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	if d.SolariaVersion != fresh.SolariaVersion {
		add("the plan was made by Solaria %s, and this is Solaria %s", d.SolariaVersion, fresh.SolariaVersion)
	}
	if d.DefinitionFormat != fresh.DefinitionFormat {
		add("the plan uses definition format %s, and this Solaria uses %s", d.DefinitionFormat, fresh.DefinitionFormat)
	}
	if d.Provider != fresh.Provider || d.Placement != fresh.Placement {
		add("the target moved from %s/%s to %s/%s", d.Provider, d.Placement, fresh.Provider, fresh.Placement)
	}
	// An empty lineage means no state yet, which a first apply changes
	// like any other: by its serial.
	switch {
	case d.State.Lineage != "" && fresh.State.Lineage == "":
		add("the state was removed since the plan, which was made from serial %d", d.State.Serial)
	case d.State.Lineage != "" && d.State.Lineage != fresh.State.Lineage:
		add("the state was replaced since the plan: lineage %s then, %s now", d.State.Lineage, fresh.State.Lineage)
	case d.State.Serial != fresh.State.Serial:
		add("the state changed since the plan: serial %d then, %d now", d.State.Serial, fresh.State.Serial)
	}

	agents := func(steps []AgentStep) map[string]AgentStep {
		m := map[string]AgentStep{}
		for _, s := range steps {
			m[s.ID] = s
		}
		return m
	}
	saved, now := agents(d.Agents), agents(fresh.Agents)
	for _, id := range slices.Sorted(maps.Keys(mergeKeys(saved, now))) {
		s, inSaved := saved[id]
		n, inNow := now[id]
		switch {
		case !inNow:
			add("%s is in the plan, and planning again doesn't find it", id)
		case !inSaved:
			add("%s isn't in the plan, and planning again finds it", id)
		case s.Action != n.Action:
			add("%s: the plan says %s, and planning again says %s", id, s.Action, n.Action)
		default:
			if ds, dn := agentDigests(s.Before), agentDigests(n.Before); ds != dn {
				add("%s: the applied agent was %s in the plan, and is %s now", id, ds, dn)
			}
			if ds, dn := agentDigests(s.After), agentDigests(n.After); ds != dn {
				add("%s: the plan applies %s, and the configuration now resolves to %s", id, ds, dn)
			}
		}
	}

	out = append(out, hostDifferences(d.Environments, fresh.Environments)...)
	out = append(out, hostDifferences(d.Vaults, fresh.Vaults)...)
	return out
}

func hostDifferences(saved, now []HostStep) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	byID := func(steps []HostStep) map[string]HostStep {
		m := map[string]HostStep{}
		for _, s := range steps {
			m[s.ID] = s
		}
		return m
	}
	sm, nm := byID(saved), byID(now)
	for _, id := range slices.Sorted(maps.Keys(mergeKeys(sm, nm))) {
		s, inSaved := sm[id]
		n, inNow := nm[id]
		switch {
		case !inNow:
			add("%s is in the plan, and planning again doesn't find it", id)
		case !inSaved:
			add("%s isn't in the plan, and planning again finds it", id)
		case s.Action != n.Action:
			add("%s: the plan says %s, and planning again says %s", id, s.Action, n.Action)
		case hostDigest(s.Before) != hostDigest(n.Before):
			add("%s: the applied object was %s in the plan, and is %s now", id, hostDigest(s.Before), hostDigest(n.Before))
		case hostDigest(s.After) != hostDigest(n.After):
			add("%s: the plan applies %s, and the configuration now gives %s", id, hostDigest(s.After), hostDigest(n.After))
		case s.Rotate != n.Rotate:
			add("%s: the plan and planning again differ on sending credential values again", id)
		}
	}
	return out
}

func mergeKeys[V any](a, b map[string]V) map[string]bool {
	m := map[string]bool{}
	for k := range a {
		m[k] = true
	}
	for k := range b {
		m[k] = true
	}
	return m
}

func agentDigests(d *AgentDigests) string {
	if d == nil {
		return "(none)"
	}
	return provider.ShortDigest(d.ResolvedDigest) + " (spec " + provider.ShortDigest(d.SpecDigest) + ")"
}

func hostDigest(d *HostDigest) string {
	if d == nil {
		return "(none)"
	}
	return provider.ShortDigest(d.Digest)
}
