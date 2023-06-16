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
	if strings.Contains(commandLine(m.Body), "\n") || !strings.Contains(m.Body, `echo ok\nApproved`) {
		t.Errorf("newline in the command not escaped: %q", m.Body)
	}
	// Box and run come from the journal path / record, but escape them too.
	r := req("r1", "Bash", "Bash: ls")
	r.Run = "run\nfake"
	if m := BuildMessage([]Request{r}, false, t0); strings.Contains(m.Title, "\n") {
		t.Errorf("title contains a raw newline: %q", m.Title)
	}
}

func TestBuildMessage_CountsBurst(t *testing.T) {
	m := BuildMessage([]Request{req("r1", "Bash", "Bash: a"), req("r2", "Bash", "Bash: b"), req("r3", "Read", "Read: /x")}, false, t0)
	if !strings.Contains(m.Title, "3 requests") {
		t.Errorf("title %q lacks the count", m.Title)
	}
}

// commandLine returns the body's command line (the line after "command: ").
func commandLine(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if c, ok := strings.CutPrefix(l, "command: "); ok {
			return c
		}
	}
	return ""
}

func TestCoalescer_OneRequestOnePushImmediately(t *testing.T) {
	c := NewCoalescer(30 * time.Second)
	pushes := c.Step("alice", map[string][]Request{"fix-bug": {req("r1", "Bash", "Bash: ls")}}, t0)
	if len(pushes) != 1 || len(pushes[0].Requests) != 1 {
		t.Fatalf("pushes = %+v, want one push with one request", pushes)
	}
	// Seen again on the next poll: no second push.
	if p := c.Step("alice", map[string][]Request{"fix-bug": {req("r1", "Bash", "Bash: ls")}}, t0.Add(5*time.Second)); len(p) != 0 {
		t.Fatalf("re-observed request pushed again: %+v", p)
	}
}

func TestCoalescer_BurstAtMostOnePushPer30sNoneDropped(t *testing.T) {
	c := NewCoalescer(30 * time.Second)
	var open []Request
	total, pushes := 0, 0
	for i := 0; i < 5; i++ {
		open = append(open, req("r"+string(rune('1'+i)), "Bash", "Bash: step"))
		now := t0.Add(time.Duration(i*2) * time.Second)
		for _, p := range c.Step("alice", map[string][]Request{"fix-bug": open}, now) {
			pushes++
			total += len(p.Requests)
		}
	}
	if pushes != 1 || total != 1 {
		t.Fatalf("within the window: %d pushes covering %d requests, want 1 push of 1", pushes, total)
	}
	// Window still open at +29 s: nothing.
	if p := c.Step("alice", map[string][]Request{"fix-bug": open}, t0.Add(29*time.Second)); len(p) != 0 {
		t.Fatalf("pushed before the window closed: %+v", p)
	}
	p := c.Step("alice", map[string][]Request{"fix-bug": open}, t0.Add(30*time.Second))
	if len(p) != 1 {
		t.Fatalf("window close: %d pushes, want 1", len(p))
	}
	total += len(p[0].Requests)
	if total != 5 {
		t.Fatalf("requests covered by pushes = %d, want 5", total)
	}
}

func TestCoalescer_RunsAreIndependent(t *testing.T) {
	c := NewCoalescer(30 * time.Second)
	p := c.Step("alice", map[string][]Request{
		"a": {req("r1", "Bash", "Bash: x")},
		"b": {req("r1", "Bash", "Bash: y")},
	}, t0)
	if len(p) != 2 {
		t.Fatalf("two runs: %d pushes, want 2", len(p))
	}
}
