package agentengine

import (
	"context"
	"errors"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		want    pb.AgentEngine
		wantErr bool
	}{
		{"claude", pb.AgentEngine_AGENT_ENGINE_CLAUDE, false},
		{"Claude", pb.AgentEngine_AGENT_ENGINE_CLAUDE, false},
		{"  codex  ", pb.AgentEngine_AGENT_ENGINE_CODEX, false},
		{"gemini", pb.AgentEngine_AGENT_ENGINE_GEMINI, false},
		{"cluade", pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED, true},
		{"", pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED, true},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("Parse(%q): want error, got nil", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q): unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParse_UnknownErrorListsValidNames(t *testing.T) {
	_, err := Parse("cluade")
	if err == nil {
		t.Fatal("want error")
	}
	for _, name := range []string{"claude", "codex", "gemini"} {
		if !contains(err.Error(), name) {
			t.Errorf("error %q should list valid name %q", err.Error(), name)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestProviderRoundTrip(t *testing.T) {
	for _, e := range All() {
		p, ok := Provider(e)
		if !ok {
			t.Errorf("Provider(%v) = (_, false), want a provider", e)
			continue
		}
		if back := ForProvider(p); back != e {
			t.Errorf("ForProvider(Provider(%v)=%q) = %v, want %v", e, p, back, e)
		}
	}
	if _, ok := Provider(pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED); ok {
		t.Error("Provider(UNSPECIFIED) should report ok=false")
	}
	if got := ForProvider("bogus"); got != pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED {
		t.Errorf("ForProvider(bogus) = %v, want UNSPECIFIED", got)
	}
}

func TestEnvValue(t *testing.T) {
	cases := map[pb.AgentEngine]string{
		pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED: "",
		pb.AgentEngine_AGENT_ENGINE_CLAUDE:      "claude",
		pb.AgentEngine_AGENT_ENGINE_CODEX:       "codex",
		pb.AgentEngine_AGENT_ENGINE_GEMINI:      "gemini",
	}
	for e, want := range cases {
		if got := EnvValue(e); got != want {
			t.Errorf("EnvValue(%v) = %q, want %q", e, got, want)
		}
	}
}

// fakeKeyResolver records every call it receives, so tests can assert which
// lookups actually happened — e.g. that a global key short-circuits an
// owner-key check rather than always performing both.
type fakeKeyResolver struct {
