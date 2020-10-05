package sentinel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshban"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// TestSSHBanAdminRoutesUseTheEnforcingProcess exercises the actual admin mux,
// including authentication, failure visibility, and targeted mutation.
func TestSSHBanAdminRoutesUseTheEnforcingProcess(t *testing.T) {
	g, err := sshban.New(sshban.Config{Listener: pb.SSHBanListener_SSH_BAN_LISTENER_MAIN, MaxFailures: 1, Duration: time.Minute, MaxEntries: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		if err := g.Check(ip + ":22"); err != nil {
			t.Fatal(err)
		}
		g.Fail(ip+":22", "auth_failure")
	}
	socket := filepath.Join(t.TempDir(), "private", "main.sock")
	stop, err := sshban.ListenControl(socket, g)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	m := newManagerForFail2BanTest(t, newFakeFail2BanRunner())
	m.hmacSecret = []byte("daemon-shared-secret-abcdefghijklmnopqrstuvwxyz")
	m.sshBanSockets = map[pb.SSHBanListener]string{pb.SSHBanListener_SSH_BAN_LISTENER_MAIN: socket}
	mux := buildBinaryServerMux("/nonexistent", m)
	const path = "/sentinel/ssh-bans/SSH_BAN_LISTENER_MAIN/192.0.2.1"
	for _, secret := range []string{"", "wrong-secret-abcdefghijklmnopqrstuvwxyz", string(m.hmacSecret)} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if secret != "" {
			r = signedGet(path, secret)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("unauthorized caller got %d: %s", w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/sentinel/ssh-bans/SSH_BAN_LISTENER_MAIN/garbage", "/sentinel/ssh-bans/SSH_BAN_LISTENER_UNSPECIFIED/192.0.2.1", "/sentinel/ssh-bans/SSH_BAN_LISTENER_MAIN?page_size=1001"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, signedGet(path, fail2banAdminSecret))
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid request got %d: %s", w.Code, w.Body.String())
		}
	}
	denied := httptest.NewRecorder()
	unsigned := httptest.NewRequest(http.MethodPost, "/sentinel/ssh-bans/SSH_BAN_LISTENER_MAIN/unban", nil)
	mux.ServeHTTP(denied, unsigned)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned mutation got %d", denied.Code)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, signedGet(path, fail2banAdminSecret))
	if w.Code != http.StatusOK {
		t.Fatalf("admin get: %d %s", w.Code, w.Body.String())
