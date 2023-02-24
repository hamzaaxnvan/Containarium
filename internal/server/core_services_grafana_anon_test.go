package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

const grafanaIniAnonOn = `[database]
type = postgres
host = 10.100.0.242:5432

[security]
allow_embedding = true
admin_user = admin

[auth.anonymous]
enabled = true
org_role = Viewer

[users]
default_theme = light
`

// The template the daemon writes on first provisioning must not enable
// anonymous access — that is the finding this fixes (#2079): with the
// dashboard port reachable from every tenant, anonymous Viewer meant any
// tenant could read platform dashboards with no credential at all.
func TestGrafanaIni_AnonymousDisabled(t *testing.T) {
	ini := renderGrafanaIni("10.100.0.242", "containarium", "secret", "adminpw")

	got, ok := grafanaAnonymousEnabled(ini)
	if !ok {
		t.Fatalf("rendered grafana.ini has no [auth.anonymous] section:\n%s", ini)
	}
	if got {
		t.Fatalf("rendered grafana.ini enables anonymous access:\n%s", ini)
	}
	if !strings.Contains(ini, "host = 10.100.0.242:5432") || !strings.Contains(ini, "password = secret") {
		t.Errorf("template lost its database settings:\n%s", ini)
	}
}

func TestDisableGrafanaAnonymous(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantChanged bool
		wantEnabled bool // state after
	}{
		{"enabled → disabled", grafanaIniAnonOn, true, false},
		{"already disabled is untouched", strings.Replace(grafanaIniAnonOn, "enabled = true", "enabled = false", 1), false, false},
		{"section absent is untouched (grafana default is disabled)", "[server]\nhttp_port = 3000\n", false, false},
		{"tolerates spacing and case", strings.Replace(grafanaIniAnonOn, "enabled = true", "Enabled=TRUE", 1), true, false},
		{"an 'enabled' key in another section is not the anonymous one",
			"[auth.basic]\nenabled = true\n\n[auth.anonymous]\nenabled = false\n", false, false},
		{"rewrites the anonymous key, not an earlier section's 'enabled'",
			"[auth.basic]\nenabled = true\n\n[auth.anonymous]\nenabled = true\n", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := disableGrafanaAnonymous(tc.in)
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v\n--- out ---\n%s", changed, tc.wantChanged, out)
			}
