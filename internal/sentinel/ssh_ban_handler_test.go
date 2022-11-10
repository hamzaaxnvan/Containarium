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
