package server

import (
	"strings"
	"testing"
)

// #2103: the daemon auto-detects an existing metrics container and then
// skips EnsureVictoriaMetrics entirely, so the #2079 backfill inside it
// never ran on any existing host. The detected path must run the same
// backfill — with or without a CoreServices already constructed.
func TestHardenDetectedGrafana(t *testing.T) {
	t.Run("no CoreServices yet: builds one and still closes anonymous access", func(t *testing.T) {
		f := newFakeGrafanaBox(grafanaIniAnonOn, nil)

		cs := hardenDetectedGrafana(nil, f)

		if cs == nil {
