package provider

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

// Action is what a plan does to one agent.
type Action string

// Actions.
const (
	NoOp    Action = "no-op"
	Create  Action = "create"
	Update  Action = "update"
	Replace Action = "replace"
	Delete  Action = "delete"
)

// Plan is what will happen to one agent.
type Plan struct {
	ID     string
	Action Action
	// Desired is nil for a delete.
	Desired *resolve.Agent
	// Prior is nil for a create.
	Prior *state.Entry
	// Changes lists what differs between Prior and Desired.
	Changes []Change
	// Reason explains an action that isn't plain from Changes, such as
	// drift or a provider change.
	Reason string
	// Notes are things the person applying should know.
	Notes []string
	// Observed is what refresh found on the host, set by the engine; nil
	// without refresh. A host with optimistic concurrency updates from it,
	// so an apply overwrites the drift the plan showed and no other.
	Observed *Observed
}

// Change is one changed field.
type Change struct {
	// Path is the field, such as model.preferred or tools.builtin.
	Path     string
	Old, New string
	// Text means Old and New summarize long text rather than show it.
	Text bool
}

// DefaultPlan compares desired with prior: create if there's no prior,
// update if the resolved digest differs, otherwise nothing.
func DefaultPlan(desired *resolve.Agent, prior *state.Entry) *Plan {
	p := &Plan{ID: desired.ID(), Desired: desired, Prior: prior}
	switch {
	case prior == nil:
		p.Action = Create
	case prior.ResolvedDigest != desired.ResolvedDigest:
		p.Action = Update
		p.Changes = Diff(&prior.Definition, &desired.Definition)
	default:
		p.Action = NoOp
	}
	return p
}

// Diff lists the fields that differ between two definitions, in field
// order. Long or multi-line text is summarized rather than shown.
func Diff(before, after *resolve.Definition) []Change {
	return DiffConfig(before, after)
}

// DiffConfig lists the fields that differ between any two values that
// encode as JSON objects, such as a target's host config.
func DiffConfig(before, after any) []Change {
	var ov, nv any
	mustRoundTrip(before, &ov)
	mustRoundTrip(after, &nv)
	// A missing config compares as an empty one, field by field.
	if ov == nil {
		ov = map[string]any{}
	}
	if nv == nil {
		nv = map[string]any{}
	}
	var out []Change
	diffValues("", ov, nv, &out)
	return out
}

func mustRoundTrip(v, into any) {
	b, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(b, into)
	}
	if err != nil {
		panic(fmt.Sprintf("value doesn't round-trip through JSON: %v", err)) // a bug, not input
	}
}

func diffValues(path string, before, after any, out *[]Change) {
	if reflect.DeepEqual(before, after) {
		return
	}
	om, oOK := before.(map[string]any)
	nm, nOK := after.(map[string]any)
	if oOK && nOK {
		keys := map[string]bool{}
		for k := range om {
			keys[k] = true
		}
		for k := range nm {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		slices.Sort(sorted)
		for _, k := range sorted {
			diffValues(join(path, k), om[k], nm[k], out)
		}
		return
	}
	if path == "instructions" {
		ol, _ := before.([]any)
		nl, _ := after.([]any)
		for i := range max(len(ol), len(nl)) {
			var o, n any
			if i < len(ol) {
				o = ol[i]
			}
			if i < len(nl) {
				n = nl[i]
			}
			if !reflect.DeepEqual(o, n) {
				*out = append(*out, Change{Path: fmt.Sprintf("instructions[%d]", i), Old: lines(o), New: lines(n), Text: true})
			}
		}
		return
	}
	*out = append(*out, Change{Path: path, Old: show(before), New: show(after)})
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// show renders a value briefly for a plan.
func show(v any) string {
	switch v := v.(type) {
	case nil:
		return "(none)"
	case string:
		if strings.HasPrefix(v, digest.Prefix) {
			return ShortDigest(v)
		}
		if len(v) > 60 || strings.Contains(v, "\n") {
			return "(" + lines(v) + ")"
		}
		return strconv.Quote(v)
	case map[string]any:
		b, _ := json.Marshal(v)
		if len(b) <= 60 {
			return string(b)
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		return "{" + strings.Join(keys, ", ") + "}"
	default:
		b, _ := json.Marshal(v)
		if len(b) > 80 {
			return fmt.Sprintf("(%d bytes)", len(b))
		}
		return string(b)
	}
}

// lines describes text by its line count, for text too long to show.
func lines(v any) string {
	s, ok := v.(string)
	if !ok {
		return "(none)"
	}
	n := strings.Count(s, "\n") + boolInt(s != "" && !strings.HasSuffix(s, "\n"))
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ShortDigest abbreviates a digest for display: sha256: and 12 hex digits.
func ShortDigest(d string) string {
	if len(d) > len(digest.Prefix)+12 {
		return d[:len(digest.Prefix)+12]
	}
	return d
}
