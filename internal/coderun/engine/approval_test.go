package engine

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/coderun"
)

func TestWithApprovalsWiresClaudePromptTool(t *testing.T) {
	eng, err := For(NameClaude, Options{Credential: SecretCredential{}})
	if err != nil {
		t.Fatal(err)
	}
	base := eng.RunCommand("do it", false, false, "")
	pub := ed25519.PublicKey(bytes.Repeat([]byte{0xab}, ed25519.PublicKeySize))
	got, ok := WithApprovals(eng, base, "my-run", pub, 30*time.Minute)
	if !ok {
		t.Fatal("WithApprovals reported Claude has no hook")
	}
	if !strings.Contains(got, base) {
