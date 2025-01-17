package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestBuildSetSSHKeysRequest(t *testing.T) {
	f := filepath.Join(t.TempDir(), "k.pub")
	if err := os.WriteFile(f, []byte("ssh-ed25519 AAAA file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := buildSetSSHKeysRequest("b", []string{"ssh-ed25519 AAAA lit"}, []string{f}, false)
	if err != nil || len(req.SshPublicKeys) != 2 || req.SshPublicKeys[1] != "ssh-ed25519 AAAA file" || req.AllowEmpty {
		t.Fatalf("req=%+v err=%v", req, err)
	}
	if req, err := buildSetSSHKeysRequest("b", nil, nil, true); err != nil || !req.AllowEmpty || len(req.SshPublicKeys) != 0 {
		t.Fatalf("explicit empty: req=%+v err=%v", req, err)
	}
	// An omitted list must never become an empty replace-all.
	if _, err := buildSetSSHKeysRequest("b", nil, nil, false); err == nil {
