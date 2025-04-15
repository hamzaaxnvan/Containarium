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

