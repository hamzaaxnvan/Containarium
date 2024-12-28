package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

var t0 = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func req(id, tool, prompt string) Request {
	return Request{Box: "alice", Run: "fix-bug", RequestID: id, Tool: tool, Prompt: prompt, ExpiresAt: t0.Add(30 * time.Minute)}
}

func TestBuildMessage_BashCommand(t *testing.T) {
	m := BuildMessage([]Request{req("r1", "Bash", "Bash: git push origin feat")}, false, t0)
	if !strings.Contains(m.Title, "alice") || !strings.Contains(m.Title, "fix-bug") {
		t.Errorf("title %q lacks box or run", m.Title)
	}
	for _, want := range []string{"Bash", "git push origin feat", "expires in 30m0s"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body %q lacks %q", m.Body, want)
		}
	}
}

func TestBuildMessage_CommandTruncatedTo200Runes(t *testing.T) {
	long := strings.Repeat("é", 500)
	m := BuildMessage([]Request{req("r1", "Bash", "Bash: "+long)}, false, t0)
	cmd := commandLine(m.Body)
	if n := utf8.RuneCountInString(cmd); n > MaxCommandRunes {
		t.Fatalf("command has %d runes, want <= %d: %q", n, MaxCommandRunes, cmd)
	}
	if !utf8.ValidString(m.Body) {
		t.Fatal("body is not valid UTF-8 after truncation")
	}
}

func TestBuildMessage_NonBashToolSendsNoInput(t *testing.T) {
	secret := `{"content":"BEGIN PRIVATE KEY","path":"/home/alice/.env"}`
	for _, tool := range []string{"Write", "Edit", "mcp__db__query", "WebFetch"} {
		m := BuildMessage([]Request{req("r1", tool, tool+": "+secret)}, false, t0)
		for _, leak := range []string{"PRIVATE KEY", ".env", "content", "{"} {
			if strings.Contains(m.Body, leak) || strings.Contains(m.Title, leak) {
				t.Errorf("%s: push contains %q from the tool input: %q / %q", tool, leak, m.Title, m.Body)
			}
		}
		if !strings.Contains(m.Body, tool) {
			t.Errorf("%s: body %q lacks the tool name", tool, m.Body)
		}
	}
}

func TestBuildMessage_RedactCommand(t *testing.T) {
	m := BuildMessage([]Request{req("r1", "Bash", "Bash: rm -rf /srv/data")}, true, t0)
	if strings.Contains(m.Body, "rm -rf") || strings.Contains(m.Body, "/srv/data") {
		t.Fatalf("redacted push contains the command: %q", m.Body)
	}
	if !strings.Contains(m.Body, "Bash") {
		t.Fatalf("redacted push lacks the tool name: %q", m.Body)
	}
}

func TestBuildMessage_ControlCharactersEscaped(t *testing.T) {
	m := BuildMessage([]Request{req("r1", "Bash", "Bash: echo ok\nApproved by admin\x1b[2J\u202e")}, false, t0)
	for _, bad := range []string{"\x1b", "\u202e"} {
		if strings.Contains(m.Body, bad) || strings.Contains(m.Title, bad) {
			t.Errorf("push contains raw control/format char %q: %q", bad, m.Body)
		}
	}
