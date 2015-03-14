// Package sshupstream exposes the sentinel's selected-routing transport hook.
package sshupstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshcontrol"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/tg123/sshpiper/libplugin"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Attempt ties one selected upstream to its originating transport connection.
type Attempt struct {
	ID, ConnectionID, RemoteAddr, URI string
}

// Dial opens the selected upstream without making an authorization decision.
type Dial func(context.Context, Attempt) (net.Conn, error)

// binding is a one-use capability created only after the child selects a route.
type binding struct {
	attempt      Attempt
	expires      time.Time
	host         string
	keys         []byte
	verify       ssh.HostKeyCallback
	conn         net.Conn
	ref          *pb.SSHUpstreamConnection
	route, lease string
	proxy        bool
	timer        *time.Timer
}

// adapter owns bounded capabilities, never the child's authorization policy.
type adapter struct {
	ctx           context.Context
	dial          Dial
	mu            sync.Mutex
	bindings      map[string]binding
	setups        map[string]binding
	childVerifier bool
	control       *sshcontrol.Peer
	routes        map[string]routeRef
}

// NewAdapter wraps the pinned routing contract and adds an attempt-correlated
// transport callback. Unknown child capabilities fail closed at startup.
func NewAdapter(ctx context.Context, child libplugin.SshPiperPluginClient, dial Dial, options ...Option) (libplugin.SshPiperPluginConfig, error) {
	cfg := libplugin.SshPiperPluginConfig{}
	if child == nil || dial == nil {
		return cfg, fmt.Errorf("router and transport dialer are required")
	}
	bootstrap, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	listed, err := child.ListCallbacks(bootstrap, &libplugin.ListCallbackRequest{})
	if err != nil {
		return cfg, err
	}
	caps := map[string]bool{}
	for _, name := range listed.GetCallbacks() {
		switch name {
		case "NewConnection", "NextAuthMethods", "NoneAuth", "PasswordAuth", "PublicKeyAuth", "KeyboardInteractiveAuth", "UpstreamAuthFailure", "Banner", "VerifyHostKey", "PipeCreateError", "PipeStart", "PipeError":
			caps[name] = true
		default:
			return cfg, fmt.Errorf("unsupported child routing capability %q", name)
		}
	}
	a := &adapter{ctx: ctx, dial: dial, bindings: make(map[string]binding), setups: make(map[string]binding), childVerifier: caps["VerifyHostKey"]}
	a.routes = make(map[string]routeRef)
	for _, option := range options {
		option(a)
	}
	cfg.CreateConnCallback = a.connect
	if caps["NewConnection"] || a.control != nil {
		cfg.NewConnectionCallback = func(c libplugin.ConnMetadata) error {
			if a.control != nil {
				if _, err := a.control.Open(ctx, c.UniqueID(), c.RemoteAddr()); err != nil {
					return err
				}
			}
			if !caps["NewConnection"] {
				return nil
			}
			_, err := child.NewConnection(ctx, &libplugin.NewConnectionRequest{Meta: metadata(c)})
			return err
		}
	}
	if caps["NextAuthMethods"] {
		cfg.NextAuthMethodsCallback = func(c libplugin.ConnMetadata) ([]string, error) {
			r, err := child.NextAuthMethods(ctx, &libplugin.NextAuthMethodsRequest{Meta: metadata(c)})
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(r.GetMethods()))
			for _, m := range r.GetMethods() {
				name := libplugin.AuthMethodTypeToName(m)
				if name == "" {
					return nil, fmt.Errorf("unsupported child auth method %v", m)
				}
				out = append(out, name)
			}
			return out, nil
		}
	}
	if caps["NoneAuth"] {
		cfg.NoClientAuthCallback = func(c libplugin.ConnMetadata) (*libplugin.Upstream, error) {
			if err := a.begin(c); err != nil {
				return nil, err
			}
			r, err := child.NoneAuth(ctx, &libplugin.NoneAuthRequest{Meta: metadata(c)})
			if err != nil {
				return nil, err
			}
			return a.bind(c, r.GetUpstream())
		}
	}
	if caps["PublicKeyAuth"] {
		cfg.PublicKeyCallback = func(c libplugin.ConnMetadata, key []byte) (*libplugin.Upstream, error) {
			if err := a.begin(c); err != nil {
				return nil, err
			}
			r, err := child.PublicKeyAuth(ctx, &libplugin.PublicKeyAuthRequest{Meta: metadata(c), PublicKey: key})
			if err != nil {
				return nil, err
			}
			return a.bind(c, r.GetUpstream())
		}
	}
	if caps["PasswordAuth"] {
		cfg.PasswordCallback = func(c libplugin.ConnMetadata, password []byte) (*libplugin.Upstream, error) {
			if err := a.begin(c); err != nil {
				return nil, err
			}
			r, err := child.PasswordAuth(ctx, &libplugin.PasswordAuthRequest{Meta: metadata(c), Password: password})
			if err != nil {
				return nil, err
			}
			return a.bind(c, r.GetUpstream())
		}
	}
	if caps["KeyboardInteractiveAuth"] {
		cfg.KeyboardInteractiveCallback = func(c libplugin.ConnMetadata, challenge libplugin.KeyboardInteractiveChallenge) (*libplugin.Upstream, error) {
			if err := a.begin(c); err != nil {
				return nil, err
			}
			stream, err := child.KeyboardInteractiveAuth(ctx)
			if err != nil {
				return nil, err
			}
			defer func() { _ = stream.CloseSend() }()
			for {
				m, err := stream.Recv()
				if err != nil {
					return nil, err
				}
				switch {
				case m.GetMetaRequest() != nil:
					err = stream.Send(&libplugin.KeyboardInteractiveAuthMessage{Message: &libplugin.KeyboardInteractiveAuthMessage_MetaResponse{MetaResponse: &libplugin.KeyboardInteractiveMetaResponse{Meta: metadata(c)}}})
				case m.GetPromptRequest() != nil:
					p := m.GetPromptRequest()
					answers := make([]string, 0, len(p.Questions))
					if len(p.Questions) == 0 {
						_, err = challenge(p.Name, p.Instruction, "", false)
					}
					for _, q := range p.Questions {
						var answer string
						answer, err = challenge(p.Name, p.Instruction, q.Text, q.Echo)
						if err != nil {
							break
						}
						answers = append(answers, answer)
					}
					if err == nil && len(p.Questions) > 0 {
						err = stream.Send(&libplugin.KeyboardInteractiveAuthMessage{Message: &libplugin.KeyboardInteractiveAuthMessage_UserResponse{UserResponse: &libplugin.KeyboardInteractiveUserResponse{Answers: answers}}})
					}
				case m.GetFinishRequest() != nil:
					return a.bind(c, m.GetFinishRequest().GetUpstream())
				default:
					return nil, fmt.Errorf("unexpected child keyboard-interactive message")
				}
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if caps["Banner"] {
		cfg.BannerCallback = func(c libplugin.ConnMetadata) string {
			r, err := child.Banner(ctx, &libplugin.BannerRequest{Meta: metadata(c)})
			if err != nil {
				return ""
			}
			return r.GetMessage()
		}
	}
	// ConnOverGrpc reports a URI as RemoteAddr; restore the selected endpoint
	// and actual socket identity before either existing verification path.
	cfg.VerifyHostKeyCallback = func(c libplugin.ConnMetadata, hostname, _ string, key []byte) (verifyErr error) {
		a.mu.Lock()
		b, ok := a.setups[c.UniqueID()]
		a.mu.Unlock()
		if !ok || b.conn == nil || hostname != b.host || c.RemoteAddr() != b.attempt.RemoteAddr || !time.Now().Before(b.expires) {
			return fmt.Errorf("host verification has no matching active attempt")
		}
		defer func() {
			if verifyErr != nil && a.control != nil {
				a.finish(c.UniqueID(), "", pb.SSHUpstreamOutcome_SSH_UPSTREAM_OUTCOME_HOST_KEY_FAILED)
			}
		}()
		if caps["VerifyHostKey"] {
			verifyCtx, cancel := context.WithDeadline(ctx, b.expires)
			defer cancel()
			r, err := child.VerifyHostKey(verifyCtx, &libplugin.VerifyHostKeyRequest{Meta: metadata(c), Hostname: b.host, Netaddress: b.conn.RemoteAddr().String(), Key: key})
			if err != nil {
				return err
			}
			if !r.GetVerified() {
				return fmt.Errorf("upstream host key rejected by router")
			}
			return nil
		}
		if len(b.keys) == 0 {
			return nil
		}
		pub, err := ssh.ParsePublicKey(key)
		if err != nil {
			return err
		}
		return b.verify(b.host, b.conn.RemoteAddr(), pub)
	}
