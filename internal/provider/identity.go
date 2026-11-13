package provider

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/solaria-sh/solaria/internal/resolve"
)

// IdentityEnv is what a run is told about the agent it runs (ADR 0008):
// its logical ID, target and digests. It changes only when they do.
func IdentityEnv(a *resolve.Agent) map[string]string {
	return map[string]string{
		"SOLARIA_LOGICAL_ID":      a.ID(),
		"SOLARIA_TARGET":          a.Target,
		"SOLARIA_SPEC_DIGEST":     a.SpecDigest,
		"SOLARIA_RESOLVED_DIGEST": a.ResolvedDigest,
	}
}

// ResourceAttributes is the value of OTEL_RESOURCE_ATTRIBUTES for a run
// (ADR 0008): the GenAI attributes, service.version and Solaria's own,
// then the target's telemetry.resourceAttributes by key. hostID is
// gen_ai.agent.id, left out when empty. Values are percent-encoded as the
// variable requires.
func ResourceAttributes(a *resolve.Agent, hostID string) string {
	var parts []string
	add := func(k, v string) { parts = append(parts, k+"="+encodeAttribute(v)) }
	if hostID != "" {
		add("gen_ai.agent.id", hostID)
	}
	add("gen_ai.agent.name", a.Name)
	add("gen_ai.agent.version", a.ResolvedDigest)
	add("service.version", a.ResolvedDigest)
	add("solaria.agent.spec_digest", a.SpecDigest)
	add("solaria.target", a.Target)
	if t := a.Env.Telemetry; t != nil {
		for _, k := range slices.Sorted(maps.Keys(t.ResourceAttributes)) {
			add(k, t.ResourceAttributes[k])
		}
	}
	return strings.Join(parts, ",")
}

// AttributeKey is the form of a key in telemetry.resourceAttributes: it
// must survive OTEL_RESOURCE_ATTRIBUTES unencoded.
var AttributeKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ReservedAttribute reports whether Solaria or the runtime sets key, so
// telemetry.resourceAttributes can't.
func ReservedAttribute(key string) bool {
	return strings.HasPrefix(key, "gen_ai.agent.") || strings.HasPrefix(key, "solaria.") ||
		key == "service.name" || key == "service.version"
}

// encodeAttribute percent-encodes the characters W3C baggage doesn't allow
// in a value, plus '%' and '=' so the value decodes unambiguously.
func encodeAttribute(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte(`",;\%=`, c) >= 0 {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
