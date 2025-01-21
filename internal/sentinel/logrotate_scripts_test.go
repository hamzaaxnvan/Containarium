//go:build !windows

package sentinel

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #2415 — both sentinel startup scripts must install a logrotate stanza for
// the SSH session sink, with a configurable retention, rotating in `create`
// mode (never copytruncate) and signalling ONLY the plugin to reopen.

const sinkPath = "/var/log/containarium/ssh-sessions.jsonl"

var sentinelScripts = []struct {
	name         string
	path         string
	retentionRef string // how `rotate <N>` references the configurable retention
	authzHUP     bool   // the unit can run ssh-authz-plugin in enforce mode
	banHUP       bool   // the unit can run ssh-ban-plugin with --records-file (#2419)
}{
	{"module", "../../terraform/modules/containarium/scripts/startup-sentinel.sh", "${ssh_session_log_retention_days}", true, true},
	{"gce", "../../terraform/gce/scripts/startup-sentinel.sh", "$SSH_SESSION_LOG_RETENTION_DAYS", false, false},
}

func stanzaOf(t *testing.T, script string) string {
	t.Helper()
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, sinkPath+" {")
	if i < 0 {
		t.Fatalf("%s has no logrotate stanza for %s", script, sinkPath)
	}
	j := strings.Index(src[i:], "\n}")
	if j < 0 {
		t.Fatalf("%s: unterminated logrotate stanza", script)
	}
	return src[i : i+j+2]
}

func TestSentinelScripts_LogrotateStanza(t *testing.T) {
	for _, sc := range sentinelScripts {
		t.Run(sc.name, func(t *testing.T) {
			st := stanzaOf(t, sc.path)
			for _, want := range []string{
				"daily", "compress", "delaycompress", "missingok", "notifempty",
				"create 0600 root root",
				"rotate " + sc.retentionRef,
				"postrotate", "endscript",
				// Anchored: sshpiperd's own command line CONTAINS the plugin's,
				// and it must not receive the SIGHUP.
				"pkill -HUP -f '" + sessionReopenPattern + "'",
			} {
				if !strings.Contains(st, want) {
					t.Errorf("stanza missing %q:\n%s", want, st)
				}
			}
			// An enforce-mode ssh-authz-plugin appends denials to the sink
			// (#2520). Only that one is signalled: a pre-enforce plugin
			// exits on SIGHUP and takes sshpiperd down with it.
			authzHUP := "pkill -HUP -f '" + authzReopenPattern + "'"
			if got := strings.Contains(st, authzHUP); got != sc.authzHUP {
				t.Errorf("stanza contains %q = %v, want %v:\n%s", authzHUP, got, sc.authzHUP, st)
			}
			// Likewise the ban plugin, only when it was given --records-file
			// (#2419): without it the plugin ignores SIGHUP, but the pattern is
			// still anchored on the flag so a plugin that predates the sink is
			// never signalled.
			banHUP := "pkill -HUP -f '" + banReopenPattern + "'"
			if got := strings.Contains(st, banHUP); got != sc.banHUP {
				t.Errorf("stanza contains %q = %v, want %v:\n%s", banHUP, got, sc.banHUP, st)
			}
			want := 1
			if sc.authzHUP {
				want++
			}
			if sc.banHUP {
