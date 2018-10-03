package container

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

func TestParseRegistryMirrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []RegistryMirror
	}{
		{"empty", "", nil},
		{"whitespace", "   ", nil},
		{
			"single http is insecure",
			"docker.io=http://mirror.lan:5000",
			[]RegistryMirror{{Upstream: "docker.io", Location: "mirror.lan:5000", Insecure: true}},
		},
		{
			"https is secure",
			"ghcr.io=https://mirror.lan:5000",
			[]RegistryMirror{{Upstream: "ghcr.io", Location: "mirror.lan:5000", Insecure: false}},
		},
		{
			"bare host is secure",
			"quay.io=mirror.lan:5000",
			[]RegistryMirror{{Upstream: "quay.io", Location: "mirror.lan:5000", Insecure: false}},
		},
		{
			"multiple, trailing slash trimmed, spaces tolerated",
			" docker.io=http://mirror.lan:5000/ , ghcr.io=mirror.lan:5000 ",
			[]RegistryMirror{
				{Upstream: "docker.io", Location: "mirror.lan:5000", Insecure: true},
				{Upstream: "ghcr.io", Location: "mirror.lan:5000", Insecure: false},
			},
		},
		{
			"malformed entries skipped (no '=', empty sides)",
			"noequals,=onlymirror,onlyupstream=,docker.io=mirror:5000",
			[]RegistryMirror{{Upstream: "docker.io", Location: "mirror:5000", Insecure: false}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRegistryMirrors(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseRegistryMirrors(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestRenderMirrorsConf(t *testing.T) {
	conf := renderMirrorsConf([]RegistryMirror{
		{Upstream: "docker.io", Location: "mirror.lan:5000", Insecure: true},
		{Upstream: "ghcr.io", Location: "mirror.lan:5000", Insecure: false},
	})
	for _, want := range []string{
		`location = "docker.io"`,
		`location = "ghcr.io"`,
		`location = "mirror.lan:5000"`,
		"[[registry.mirror]]",
		"insecure = true",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("rendered conf missing %q:\n%s", want, conf)
		}
	}
	// The secure (ghcr.io) mirror must NOT carry insecure; exactly one insecure
	// line for the single insecure entry.
	if n := strings.Count(conf, "insecure = true"); n != 1 {
