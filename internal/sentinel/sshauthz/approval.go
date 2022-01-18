package sshauthz

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/tg123/sshpiper/libplugin"
	"google.golang.org/protobuf/proto"
)

// Device approval (#2526, design component 3). An APPROVAL_PENDING answer in
// enforce mode keeps sshpiperd's chain cursor on this plugin with a retry;
// sshpiperd turns it into publickey partial success once the key has signed,
// and keyboard-interactive shows the URL and code, waits for Enter, and polls.
const (
	approvalPrompt      = "press Enter once approved: "
	defaultApprovalPoll = 2 * time.Second
	minApprovalPoll     = 250 * time.Millisecond
	// Caps on connections in the approval stage at once (#3084): a key that
	// signed can hold at most this many polls per login and per source IP.
	// Beyond them the login is refused at once; nothing waits for a slot.
	maxApprovalsPerLogin = 3
	maxApprovalsPerIP    = 10
)

// pendingApproval is kept in the plugin by connection id, never in sshpiperd
// metadata: sshpiperd stores a retry's metadata even for an unsigned publickey
// query, so state there would let a client that queried someone else's public
// key reach the prompt after signing with another key. Every publickey offer
// clears its connection's approval first, so the approval present at
// keyboard-interactive belongs to the key that signed.
type pendingApproval struct {
	id, url, code string
	host          string // the URL's host, the only part of it that is logged
	poll          time.Duration
	deadline      time.Time // min(expires_at, first PENDING + MaxApprovalWait)
	backend, fp   string
	// session, login and addr identify the connection in the denied record of
	// a prompt never answered; user is the daemon's user_id with the PENDING.
	session, login, addr, user string
	wire                       []byte
	req                        *pb.AuthorizeSSHRequest
}

// newApproval returns the approval to prompt for, or false when the daemon's
// answer cannot be shown safely; the caller then denies as before.
func newApproval(res *pb.AuthorizeSSHResponse, now time.Time) (pendingApproval, bool) {
	a := res.GetApproval()
	if res.GetDenyReason() != pb.SSHDenyReason_SSH_DENY_REASON_APPROVAL_PENDING || a == nil ||
		!printable(a.GetApprovalId(), 256) || !printable(a.GetCode(), 64) || !printable(a.GetUrl(), 2048) {
		return pendingApproval{}, false
	}
	// The URL and code reach the user's terminal: https only, no control
	// characters (printable), nothing that could pass for a different site.
	u, err := url.Parse(a.GetUrl())
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return pendingApproval{}, false
	}
	if err := a.GetExpiresAt().CheckValid(); err != nil {
		return pendingApproval{}, false
	}
	poll := defaultApprovalPoll
	if d := a.GetPollAfter(); d != nil {
		if d.CheckValid() != nil {
			return pendingApproval{}, false
		}
		if d.AsDuration() > 0 {
			poll = max(d.AsDuration(), minApprovalPoll)
		}
	}
	deadline := a.GetExpiresAt().AsTime()
	if capped := now.Add(MaxApprovalWait); capped.Before(deadline) {
		deadline = capped
	}
	if !now.Before(deadline) {
		return pendingApproval{}, false
	}
