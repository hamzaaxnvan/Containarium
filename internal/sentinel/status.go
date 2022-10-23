package sentinel

import (
	_ "embed"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed status.html
var statusPageHTML string

var statusTemplate = template.Must(template.New("status").Parse(statusPageHTML))

// StatusData holds the data rendered into the status page template.
type StatusData struct {
	State          string
	SpotIP         string
	ForwardedPorts string
	PreemptCount   int
	LastPreemption string
	OutageDuration string
	CertSyncCount  int
	CertLastSync   string
	CertSyncError  string
	KeySyncCount   int
	KeyLastSync    string
	KeySyncError   string

	// Tunnel sessions over TLS (tunnel_sessions_tls on /metrics).
	TunnelSessionsTLS int
}

// StatusHandler returns an HTTP handler that renders the sentinel status page.
func StatusHandler(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := StatusData{
			State:          string(m.CurrentState()),
			SpotIP:         m.SpotIP(),
			ForwardedPorts: formatPorts(m.config.ForwardedPorts),
			PreemptCount:   m.PreemptCount(),
		}

		if lp := m.LastPreemption(); !lp.IsZero() {
			data.LastPreemption = lp.Format(time.RFC3339)
