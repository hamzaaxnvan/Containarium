package sshban

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/tg123/sshpiper/libplugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestPluginCallbacksPreserveBothFailureKinds pins upstream callback accounting
// while ensuring credential acceptance remains with the routing plugin.
func TestPluginCallbacksPreserveBothFailureKinds(t *testing.T) {
	g, _ := fixture(t, nil)
	c := g.PluginConfig()
	if c.NewConnectionCallback == nil || c.UpstreamAuthFailureCallback == nil || c.PipeCreateErrorCallback == nil || c.NoClientAuthCallback == nil {
		t.Fatal("missing enforcement callbacks")
	}
	conn := &libplugin.ConnMeta{FromAddr: "192.0.2.1:1234"}
	if err := c.NewConnectionCallback(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := c.NoClientAuthCallback(conn); err != nil {
		t.Fatal(err)
	}
	c.UpstreamAuthFailureCallback(conn, "publickey", errors.New("rejected"), nil)
	c.PipeCreateErrorCallback(conn.RemoteAddr(), errors.New("pipe failed"))
	if err := c.NewConnectionCallback(conn); err == nil {
		t.Fatal("callbacks failed to ban")
	}
	if n := snapshot(t, g, "192.0.2.1").Status.GetFailures(); n != 2 {
		t.Fatalf("callback accounting changed: %d", n)
	}
	if c.PublicKeyCallback != nil || c.PasswordCallback != nil {
		t.Fatal("ban plugin must not take over credential authorization")
	}
}

// TestControlSocketQueriesEnforcementAndTargetsUnban drives the generated client
// through a real private socket, including pagination and listener ownership.
func TestControlSocketQueriesEnforcementAndTargetsUnban(t *testing.T) {
	g, _ := fixture(t, nil)
	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		if err := g.Check(ip + ":22"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			g.Fail(ip+":22", "auth_failure")
