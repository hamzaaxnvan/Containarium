package engine

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/coderun"
)

// TestRunCommand is the #1727 AC's table: every (engine × streamJSON ×
// continue × credential) combination, with the exact command string pinned.
//
// Pinning the whole string rather than substring-matching is deliberate. The
// command is assembled by string concatenation and then handed to `/bin/sh
// -c` on a box, so the two things that can go wrong are a missing flag and a
// quoting slip — and only an exact comparison catches the second.
func TestRunCommand(t *testing.T) {
	const prompt = "fix the bug"

	// The three prefixes, written out once so a test failure shows which
	// credential source produced the wrong one.
	const secretPrefix = `set -a; [ -f /run/containarium/secrets.env ] && . /run/containarium/secrets.env; set +a; `
	const claudeGatewayPrefix = `set -a; [ -f "$HOME/.claude/gateway.env" ] && . "$HOME/.claude/gateway.env"; set +a; `
	const piGatewayPrefix = `set -a; [ -f "$HOME/.pi/gateway.env" ] && . "$HOME/.pi/gateway.env"; set +a; `
	const codexGatewayPrefix = `set -a; [ -f "$HOME/.codex/gateway.env" ] && . "$HOME/.codex/gateway.env"; set +a; `

	// Claude's own body is unchanged from BuildClaudeRunCommand — the
	// conditional --mcp-config dance included.
	const claudeBody = `mcpcfg=; [ -f "$HOME/.claude/containarium-mcp.json" ] && mcpcfg="--mcp-config $HOME/.claude/containarium-mcp.json"; ~/.local/bin/claude $mcpcfg -p 'fix the bug'`

	tests := []struct {
		name       string
		engine     Name
		credential CredentialSource
		model      string
		streamJSON bool
		continues  bool
		sessionID  string
		want       string
	}{
		// ---- claude × secret (today's shipped behaviour) ----
		{
			name:       "claude/secret/plain",
			engine:     NameClaude,
			credential: SecretCredential{},
			want:       secretPrefix + claudeBody,
		},
		{
			name:       "claude/secret/streamJSON",
			engine:     NameClaude,
			credential: SecretCredential{},
			streamJSON: true,
			want:       secretPrefix + claudeBody + ` --output-format stream-json`,
		},
		{
			name:       "claude/secret/continue",
			engine:     NameClaude,
			credential: SecretCredential{},
			continues:  true,
			want:       secretPrefix + claudeBody + ` --continue`,
		},
		{
			name:       "claude/secret/streamJSON+continue",
			engine:     NameClaude,
			credential: SecretCredential{},
			streamJSON: true,
			continues:  true,
			want:       secretPrefix + claudeBody + ` --output-format stream-json --continue`,
		},

		// ---- claude × gateway ----
		{
			name:       "claude/gateway/plain",
			engine:     NameClaude,
			credential: GatewayCredential{Provider: "anthropic"},
			want:       claudeGatewayPrefix + claudeBody,
		},
		{
			name:       "claude/gateway/streamJSON+continue",
			engine:     NameClaude,
			credential: GatewayCredential{Provider: "anthropic"},
			streamJSON: true,
			continues:  true,
			want:       claudeGatewayPrefix + claudeBody + ` --output-format stream-json --continue`,
		},

		// ---- pi × gateway (the #1727 headline path) ----
		{
			name:       "pi/gateway/plain",
			engine:     NamePi,
			credential: GatewayCredential{Provider: "kafeido"},
			want:       piGatewayPrefix + `~/.local/bin/pi -p 'fix the bug'`,
		},
		{
			name:       "pi/gateway/streamJSON",
			engine:     NamePi,
			credential: GatewayCredential{Provider: "kafeido"},
			streamJSON: true,
			want:       piGatewayPrefix + `~/.local/bin/pi -p 'fix the bug' --mode json`,
		},
		{
			// --continue maps to `pi -c`, per the AC.
			name:       "pi/gateway/continue",
			engine:     NamePi,
			credential: GatewayCredential{Provider: "kafeido"},
			continues:  true,
			want:       piGatewayPrefix + `~/.local/bin/pi -p 'fix the bug' -c`,
		},
		{
			name:       "pi/gateway/streamJSON+continue",
			engine:     NamePi,
			credential: GatewayCredential{Provider: "kafeido"},
			streamJSON: true,
			continues:  true,
			want:       piGatewayPrefix + `~/.local/bin/pi -p 'fix the bug' --mode json -c`,
		},
		{
			// The model from code.json becomes pi's --model, so a run cannot
			// silently drift off the model the gateway token allows.
			name:       "pi/gateway/model",
			engine:     NamePi,
			credential: GatewayCredential{Provider: "kafeido"},
			model:      "kafeido-coder",
			want:       piGatewayPrefix + `~/.local/bin/pi -p 'fix the bug' --model 'kafeido-coder'`,
		},

		// ---- pi × secret ----
		{
			name:       "pi/secret/plain",
			engine:     NamePi,
			credential: SecretCredential{Name: "ANTHROPIC_API_KEY"},
			want:       secretPrefix + `~/.local/bin/pi -p 'fix the bug'`,
		},
		{
			name:       "pi/secret/streamJSON+continue+model",
			engine:     NamePi,
			credential: SecretCredential{Name: "ANTHROPIC_API_KEY"},
			model:      "claude-sonnet-5",
			streamJSON: true,
			continues:  true,
			want:       secretPrefix + `~/.local/bin/pi -p 'fix the bug' --mode json -c --model 'claude-sonnet-5'`,
		},

		// ---- #2193: --session / session_id resume ----
		{
			name:       "claude/secret/session",
			engine:     NameClaude,
			credential: SecretCredential{},
			sessionID:  "abc-123",
			want:       secretPrefix + claudeBody + ` --resume 'abc-123'`,
		},
		{
			name:       "claude/secret/session takes priority over continue",
			engine:     NameClaude,
			credential: SecretCredential{},
			sessionID:  "abc-123",
			continues:  true,
			want:       secretPrefix + claudeBody + ` --resume 'abc-123'`,
		},
		{
			name:       "claude/gateway/session",
			engine:     NameClaude,
			credential: GatewayCredential{Provider: "anthropic"},
			sessionID:  "abc-123",
			streamJSON: true,
			want:       claudeGatewayPrefix + claudeBody + ` --output-format stream-json --resume 'abc-123'`,
		},
		{
			// The quote-breaking id is the injection half of "quoting
			// pinned": a session id that tries to terminate the quoted
			// string early must come back single-quoted just like a prompt.
			name:       "claude/secret/session with a single quote",
			engine:     NameClaude,
			credential: SecretCredential{},
			sessionID:  "'; touch /tmp/pwned; echo '",
			want:       secretPrefix + claudeBody + ` --resume ''\''; touch /tmp/pwned; echo '\'''`,
		},
		{
			name:       "pi/secret/session",
			engine:     NamePi,
			credential: SecretCredential{Name: "ANTHROPIC_API_KEY"},
			sessionID:  "sess-9",
			want:       secretPrefix + `~/.local/bin/pi -p 'fix the bug' --session 'sess-9'`,
		},
		{
			name:       "pi/gateway/session takes priority over continue, before --model",
			engine:     NamePi,
			credential: GatewayCredential{Provider: "kafeido"},
			sessionID:  "sess-9",
			continues:  true,
			model:      "kafeido-coder",
			want:       piGatewayPrefix + `~/.local/bin/pi -p 'fix the bug' --session 'sess-9' --model 'kafeido-coder'`,
