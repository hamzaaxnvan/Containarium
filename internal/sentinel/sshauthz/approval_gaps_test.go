package sshauthz

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/tg123/sshpiper/libplugin"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/types/known/durationpb"
)

// A full approval store denies the offer instead of prompting (#3083). The
// store is bounded so a flood of unsigned publickey queries cannot grow the
// plugin without limit; entries past their deadline are swept first, so
// expired approvals never hold the store full.
func TestDeviceApprovalStoreIsBounded(t *testing.T) {
	f := newRouterFixture(t, "")
	wire := f.alice.PublicKey().Marshal()
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	fill := func(h *approvalHarness, deadline time.Time) {
		for i := range maxPending {
			h.p.approvals[fmt.Sprint("other-", i)] = pendingApproval{deadline: deadline}
		}
	}
	t.Run("a full store denies the offer", func(t *testing.T) {
		h := newApprovalHarness(t, f, true, pendingRes(start.Add(5*time.Minute), 5*time.Second), nil, nil)
		fill(h, start.Add(time.Minute))
		conn := &libplugin.ConnMeta{UserName: "alice", UniqId: "conn-1"}
		up, err := h.cfg.PublicKeyCallback(conn, wire)
		if err == nil || up != nil {
			t.Fatalf("a full store must deny: %v %v", up, err)
		}
		if m := h.methods(t, conn); m != "publickey" {
			t.Fatalf("methods = %q, a denied offer must not enter the approval stage", m)
		}
		if h.p.hasApproval("conn-1") || len(h.p.approvals) != maxPending {
			t.Fatalf("store holds %d approvals, want %d and none for conn-1", len(h.p.approvals), maxPending)
		}
		if n := len(h.sink.recs); n != 1 || h.sink.recs[0].Phase != sshsession.SessionPhaseDenied || h.sink.recs[0].DenyReason != pb.SSHDenyReason_SSH_DENY_REASON_APPROVAL_PENDING {
			t.Fatalf("records: %+v", h.sink.recs)
		}
		if logs := strings.Join(h.logs, "\n"); !strings.Contains(logs, "too many pending device approvals") {
			t.Fatalf("logs: %s", logs)
		}
		if _, err := h.cfg.KeyboardInteractiveCallback(conn, func(string, string, string, bool) (string, error) {
			t.Fatal("no challenge expected")
			return "", nil
		}); err == nil {
			t.Fatal("keyboard-interactive must be refused")
		}
		if len(h.reqs) != 1 {
			t.Fatalf("Authorize calls = %d, want only the offer", len(h.reqs))
		}
	})
	t.Run("expired approvals are swept to make room", func(t *testing.T) {
		h := newApprovalHarness(t, f, true, pendingRes(start.Add(5*time.Minute), 5*time.Second), nil, nil)
		fill(h, start) // deadline == now: expired
		conn := &libplugin.ConnMeta{UserName: "alice", UniqId: "conn-1"}
		up, err := h.cfg.PublicKeyCallback(conn, wire)
		if err != nil || up.GetRetryCurrentPlugin() == nil {
			t.Fatalf("expired entries must not block a new approval: %v %v", up, err)
		}
		if len(h.p.approvals) != 1 || !h.p.hasApproval("conn-1") {
			t.Fatalf("store holds %d approvals, want only conn-1", len(h.p.approvals))
		}
	})
}

// The approval URL reaches the user's terminal; a URL with userinfo can pass
// for a different site (https://approve.example.com@evil.example/), so it is
// refused at the offer like plain http.
func TestDeviceApprovalRefusesUserinfoInURL(t *testing.T) {
	f := newRouterFixture(t, "")
	wire := f.alice.PublicKey().Marshal()
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	for _, u := range []string{
		"https://approve.example.com@evil.example/d",
		"https://user:secret@approve.example.com/d",
		"https://:secret@approve.example.com/d",
	} {
		t.Run(u, func(t *testing.T) {
			res := pendingRes(start.Add(5*time.Minute), 5*time.Second)
			res.Approval.Url = u
			h := newApprovalHarness(t, f, true, res, nil, nil)
			conn := &libplugin.ConnMeta{UserName: "alice", UniqId: "conn-1"}
			if up, err := h.cfg.PublicKeyCallback(conn, wire); err == nil || up != nil {
				t.Fatalf("a URL with userinfo must be refused at the offer: %v %v", up, err)
			}
			if m := h.methods(t, conn); m != "publickey" {
				t.Fatalf("methods = %q", m)
			}
			if h.p.hasApproval("conn-1") {
				t.Fatal("approval stored")
			}
		})
	}
	if _, ok := newApproval(pendingRes(start.Add(time.Minute), time.Second), start); !ok {
		t.Fatal("control: the same response without userinfo must be accepted")
	}
