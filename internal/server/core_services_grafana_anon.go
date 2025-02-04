package server

import (
	"fmt"
	"log"
	"strings"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// grafanaIniPath is where the daemon writes Grafana's config inside the
// core-victoriametrics container.
const grafanaIniPath = "/etc/grafana/grafana.ini"

// renderGrafanaIni is the grafana.ini the daemon provisions. Anonymous
// access is off: the dashboard port is on the same bridge as every tenant
// (see docs/security/multi-tenant-isolation.md), so an anonymous Viewer
// role would let any tenant read platform dashboards with no credential at
// all (#2079). Dashboards are reached through Caddy with a login.
func renderGrafanaIni(postgresIP, dbUser, dbPassword, adminPassword string) string {
	// An empty adminPassword omits the key: Grafana then uses its own default
	// login and prompts for a change at first login, rather than this template
	// shipping a literal one (#2091).
	adminLine := ""
	if adminPassword != "" {
		adminLine = "admin_password = " + grafanaIniValue(adminPassword) + "\n"
	}
	return fmt.Sprintf(`[database]
type = postgres
host = %s:5432
name = grafana
