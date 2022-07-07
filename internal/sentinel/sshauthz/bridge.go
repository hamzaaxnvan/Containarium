package sshauthz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const DefaultSocket = "/run/containarium/ssh-authz.sock"

type route struct {
	Backend string `json:"backend"`
	Known   bool   `json:"known"`
}

type readiness struct {
	Ready bool `json:"ready"`
}

// BridgeClient joins the separate sshpiperd plugin process to the manager's
// live keystore. Only the manager needs access to sentinel signing secrets.
type BridgeClient struct{ client *http.Client }

func NewBridgeClient(socket string) *BridgeClient {
	return &BridgeClient{client: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}}
}

func (b *BridgeClient) ResolveBackend(ctx context.Context, login string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://sentinel/route?login="+url.QueryEscape(login), nil)
	if err != nil {
		return "", false, err
	}
	res, err := b.client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", false, &HTTPError{Status: res.StatusCode}
	}
	var out route
	if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&out); err != nil {
		return "", false, err
	}
	if out.Known && out.Backend == "" {
		return "", false, errors.New("empty backend in route")
	}
	return out.Backend, out.Known, nil
}

// RoutingReady asks the manager whether its keystore has synced every healthy
// backend. Any failure reads as not ready: the enforcing plugin refuses the
// unknown login either way, and "not synced" is the safer thing to log.
func (b *BridgeClient) RoutingReady(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://sentinel/ready", nil)
	if err != nil {
		return false
	}
	res, err := b.client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}
	var out readiness
	if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&out); err != nil {
		return false
	}
	return out.Ready
}

func (b *BridgeClient) Authorize(ctx context.Context, backend string, in *pb.AuthorizeSSHRequest) (*pb.AuthorizeSSHResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
