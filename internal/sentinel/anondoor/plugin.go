// Package anondoor is the sshpiperd plugin behind `ssh new.<cloud-domain>`
// (docs/architecture/ssh-new-anonymous-box.md, #2198): the sentinel-side
// half of the anonymous-box door.
//
// It runs in a SECOND sshpiperd instance bound to the door's address,
// chained after the sshsession audit plugin. On public-key auth it asks
// the anon-pool daemon's AnonymousBoxService for "the box for this key"
// (POST /v1/anon/boxes:ensure, signed with the sentinel's own request
// signature — no new secret) and hands sshpiper an upstream pipe to it,
// authenticated with the same /etc/sshpiper/upstream_key every tenant
// pipe uses. sshpiper does the proxying, host keys, fail2ban and, via
// sshsession, the audit record. Any non-OK answer is a rejected auth, so
// the door never pipes to a half-provisioned box.
//
// Only public keys are accepted: the key IS the identity. Password and
// keyboard-interactive are refused, and the banner says so, since the
// rejection reason of a failed public-key auth is not something the SSH
// protocol delivers to the client (it sees "Permission denied"); the
// reason is logged on the sentinel and carried in the sshsession record.
package anondoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tg123/sshpiper/libplugin"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const (
	// EnsurePath is grpc-gateway's mapping of AnonymousBoxService.EnsureAnonymousBox.
	EnsurePath = "/v1/anon/boxes:ensure"
	// DefaultUpstreamKeyPath is the key sshpiper authenticates to every box with.
	DefaultUpstreamKeyPath = "/etc/sshpiper/upstream_key"
	// DefaultTimeout bounds one Ensure call: a cold VM boot sits inside it,
	// and it must stay under sshpiperd's login grace so a slow daemon is a
	// clean rejection rather than a hung client.
	DefaultTimeout = 90 * time.Second
	// DefaultBanner is shown before authentication.
	DefaultBanner = "Containarium anonymous box: connect with an SSH key (no password).\n" +
		"If you are refused, the door may be closed or full - try again in a minute.\n"
)

// Config is everything the plugin needs.
type Config struct {
	// DaemonURL is the anon-pool daemon's REST base, e.g. http://<ip>:8080.
	DaemonURL string
	// UpstreamKeyPath is the private key the pipe authenticates with.
	UpstreamKeyPath string
	// Sign stamps the sentinel's request signature; nil sends unsigned
	// (tests), which a real daemon rejects.
	Sign func(*http.Request)
	// Timeout bounds one Ensure call. 0 = DefaultTimeout.
	Timeout time.Duration
	// Banner is sent to the client before auth. "" = DefaultBanner.
	Banner string
	// HTTPClient overrides the transport; nil = http.DefaultTransport.
	HTTPClient *http.Client
	// Logf receives one line per decision; nil = silent.
	Logf func(format string, args ...any)
	// RegisterRoute commits the actual daemon response to private upstream control.
	RegisterRoute func(libplugin.ConnMetadata, *pb.EnsureAnonymousBoxResponse) error
}
