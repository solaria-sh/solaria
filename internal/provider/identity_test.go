package provider

import (
	"testing"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/resolve"
)

func TestResourceAttributes(t *testing.T) {
	a := &resolve.Agent{
		Name:           "triage",
		Target:         "prod",
		SpecDigest:     "sha256:aa",
		ResolvedDigest: "sha256:bb",
		Env: &v1alpha1.TargetSpec{Telemetry: &v1alpha1.TelemetrySpec{
			Endpoint: "http://collector:4318",
			ResourceAttributes: map[string]string{
				"team":              "code review, core",
				"service.namespace": "platform",
				"odd":               `50%="x";\`,
			},
		}},
	}
	want := "gen_ai.agent.id=img,gen_ai.agent.name=triage,gen_ai.agent.version=sha256:bb,service.version=sha256:bb," +
		"solaria.agent.spec_digest=sha256:aa,solaria.target=prod," +
		"odd=50%25%3D%22x%22%3B%5C,service.namespace=platform,team=code%20review%2C%20core"
	if got := ResourceAttributes(a, "img"); got != want {
		t.Errorf("ResourceAttributes:\n got %s\nwant %s", got, want)
	}

	id := IdentityEnv(a)
	for k, v := range map[string]string{
		"SOLARIA_LOGICAL_ID": "Agent/triage", "SOLARIA_TARGET": "prod",
		"SOLARIA_SPEC_DIGEST": "sha256:aa", "SOLARIA_RESOLVED_DIGEST": "sha256:bb",
	} {
		if id[k] != v {
			t.Errorf("IdentityEnv[%s] = %q, want %q", k, id[k], v)
		}
	}
	if len(id) != 4 {
		t.Errorf("IdentityEnv has %d variables, want 4", len(id))
	}
}

func TestReservedAttribute(t *testing.T) {
	for k, want := range map[string]bool{
		"gen_ai.agent.name": true, "gen_ai.agent.custom": true, "solaria.anything": true,
		"service.name": true, "service.version": true,
		"service.namespace": false, "gen_ai.system": false, "team": false,
	} {
		if got := ReservedAttribute(k); got != want {
			t.Errorf("ReservedAttribute(%q) = %v, want %v", k, got, want)
		}
	}
}
