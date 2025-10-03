//go:build !containarium_client

package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/notify"
	"github.com/footprintai/containarium/internal/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeNotifySecrets is an in-memory secrets store with the real store's
// broker-only rules: Get hides a broker value, BrokerCredential reads only one.
type fakeNotifySecrets struct {
	mu         sync.Mutex
	rows       map[string]struct{ value, delivery string }
	failSet    bool
	failDelete int // fail this many Delete calls
}

func (f *fakeNotifySecrets) List(_ context.Context, u string) ([]secrets.SecretMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []secrets.SecretMetadata
	for k, r := range f.rows {
		if owner, name, _ := strings.Cut(k, "/"); owner == u {
			out = append(out, secrets.SecretMetadata{Username: u, Name: name, Delivery: r.delivery})
		}
	}
	return out, nil
}

// tokenRows returns u's channel-token secrets, by name.
func (f *fakeNotifySecrets) tokenRows(u string) map[string]struct{ value, delivery string } {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]struct{ value, delivery string }{}
	for k, r := range f.rows {
		if owner, name, _ := strings.Cut(k, "/"); owner == u && strings.HasPrefix(name, notify.TokenSecretPrefix) {
			out[name] = r
		}
	}
	return out
}

func newFakeNotifySecrets() *fakeNotifySecrets {
	return &fakeNotifySecrets{rows: map[string]struct{ value, delivery string }{}}
}

func (f *fakeNotifySecrets) Get(_ context.Context, u, n string) (*secrets.SecretMetadata, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[u+"/"+n]
	if !ok {
		return nil, "", secrets.ErrNotFound
	}
	if r.delivery == secrets.DeliveryBroker {
		return &secrets.SecretMetadata{}, "", secrets.ErrBrokerOnly
	}
	return &secrets.SecretMetadata{}, r.value, nil
}

func (f *fakeNotifySecrets) Set(_ context.Context, u, n, v, d string) (*secrets.SecretMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSet {
		return nil, errors.New("secrets store down")
	}
	f.rows[u+"/"+n] = struct{ value, delivery string }{v, d}
	return &secrets.SecretMetadata{}, nil
}

func (f *fakeNotifySecrets) Delete(_ context.Context, u, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDelete > 0 {
		f.failDelete--
		return errors.New("secrets store down")
	}
	if _, ok := f.rows[u+"/"+n]; !ok {
		return secrets.ErrNotFound
	}
	delete(f.rows, u+"/"+n)
	return nil
}

func (f *fakeNotifySecrets) BrokerCredential(_ context.Context, u, n string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[u+"/"+n]
	if !ok {
		return "", secrets.ErrNotFound
	}
	if r.delivery != secrets.DeliveryBroker {
		return "", secrets.ErrNotBrokerOnly
	}
	return r.value, nil
}

type sentPush struct {
	ch    notify.Channel
	token string
	msg   notify.Message
}

type fakeSender struct {
	mu   sync.Mutex
	sent []sentPush
	err  error
	// block, when set, is called before a send and may wait.
	block func(ch notify.Channel)
}

func (f *fakeSender) Send(_ context.Context, ch notify.Channel, token string, m notify.Message) error {
	if f.block != nil {
		f.block(ch)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentPush{ch, token, m})
	return nil
}

func (f *fakeSender) all() []sentPush {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentPush(nil), f.sent...)
}

func notifyCtx(user string) context.Context {
	return auth.ContextWithTestSubject(context.Background(), user, "user")
}

func newTestNotifyServer(t *testing.T) (*NotifyServer, *fakeNotifySecrets, *fakeSender, *shBox, string) {
	t.Helper()
	b := &shBox{}
	dir := t.TempDir()
	sec := newFakeNotifySecrets()
	snd := &fakeSender{}
	s := NewNotifyServer(notify.NewMemStore(), sec, func() boxScriptFunc { return b.exec }, dir, nil)
	s.sender = snd
	return s, sec, snd, b, dir
}

func TestSetNotifyChannel_TokenStoredBrokerOnlyNeverReturned(t *testing.T) {
	s, sec, _, _, _ := newTestNotifyServer(t)
	resp, err := s.SetNotifyChannel(notifyCtx("alice"), &pb.SetNotifyChannelRequest{
		Kind: pb.NotifyChannelKind_NOTIFY_CHANNEL_KIND_NTFY, Server: "https://ntfy.example.com", Topic: "alice-runs", Token: "tk_123",
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := resp.GetChannel()
	if ch.GetUsername() != "alice" || !ch.GetHasToken() {
		t.Fatalf("channel = %v", ch)
	}
	if strings.Contains(ch.String(), "tk_123") {
		t.Fatal("response carries the token")
	}
	rows := sec.tokenRows("alice")
	if len(rows) != 1 {
		t.Fatalf("token secrets = %v, want one", rows)
	}
	for _, row := range rows {
		if row.value != "tk_123" || row.delivery != secrets.DeliveryBroker {
			t.Fatalf("stored secret = %+v, want the token in broker-only delivery", row)
		}
	}

	// Updating without a token keeps it; clear_token removes it.
	resp, err = s.SetNotifyChannel(notifyCtx("alice"), &pb.SetNotifyChannelRequest{
		Kind: pb.NotifyChannelKind_NOTIFY_CHANNEL_KIND_NTFY, Server: "https://ntfy.example.com", Topic: "alice-2",
	})
	if err != nil || !resp.GetChannel().GetHasToken() {
		t.Fatalf("update without token: %v %v", resp, err)
	}
	resp, err = s.SetNotifyChannel(notifyCtx("alice"), &pb.SetNotifyChannelRequest{
		Kind: pb.NotifyChannelKind_NOTIFY_CHANNEL_KIND_NTFY, Server: "https://ntfy.example.com", Topic: "alice-2", ClearToken: true,
	})
	if err != nil || resp.GetChannel().GetHasToken() {
		t.Fatalf("clear_token: %v %v", resp, err)
	}
	if rows := sec.tokenRows("alice"); len(rows) != 0 {
		t.Fatalf("clear_token left %v", rows)
	}
}

func TestSetNotifyChannel_Refusals(t *testing.T) {
	s, sec, _, _, _ := newTestNotifyServer(t)
	ntfy := pb.NotifyChannelKind_NOTIFY_CHANNEL_KIND_NTFY
	cases := []struct {
		name string
		ctx  context.Context
		req  *pb.SetNotifyChannelRequest
		code codes.Code
	}{
		{"other tenant", notifyCtx("alice"), &pb.SetNotifyChannelRequest{Username: "bob", Kind: ntfy, Server: "https://n.example.com", Topic: "t"}, codes.PermissionDenied},
		{"no kind", notifyCtx("alice"), &pb.SetNotifyChannelRequest{Server: "https://n.example.com", Topic: "t"}, codes.InvalidArgument},
		{"token over http", notifyCtx("alice"), &pb.SetNotifyChannelRequest{Kind: ntfy, Server: "http://n.example.com", Topic: "t", Token: "x"}, codes.InvalidArgument},
		{"token and clear", notifyCtx("alice"), &pb.SetNotifyChannelRequest{Kind: ntfy, Server: "https://n.example.com", Topic: "t", Token: "x", ClearToken: true}, codes.InvalidArgument},
		{"bad topic", notifyCtx("alice"), &pb.SetNotifyChannelRequest{Kind: ntfy, Server: "https://n.example.com", Topic: "a/b"}, codes.InvalidArgument},
		{"wrong scope", ctxWithScopes("alice", false, auth.ScopeSecretsRead), &pb.SetNotifyChannelRequest{Kind: ntfy, Server: "https://n.example.com", Topic: "t"}, codes.PermissionDenied},
	}
	for _, c := range cases {
		if _, err := s.SetNotifyChannel(c.ctx, c.req); status.Code(err) != c.code {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.code)
		}
	}
	// A same-prefixed secret the box receives was not written here: setting
	// and clearing tokens never touches it.
	name := notify.TokenSecretPrefix + "MINE"
	sec.rows["alice/"+name] = struct{ value, delivery string }{"old", secrets.DeliveryEnv}
	for _, req := range []*pb.SetNotifyChannelRequest{
		{Kind: ntfy, Server: "https://n.example.com", Topic: "t", Token: "new"},
		{Kind: ntfy, Server: "https://n.example.com", Topic: "t", ClearToken: true},
	} {
		if _, err := s.SetNotifyChannel(notifyCtx("alice"), req); err != nil {
			t.Fatal(err)
		}
	}
	if r := sec.rows["alice/"+name]; r.value != "old" || r.delivery != secrets.DeliveryEnv {
		t.Fatalf("delivering secret changed: %+v", r)
	}
}

func TestDeleteNotifyChannel_RemovesToken(t *testing.T) {
	s, sec, _, _, _ := newTestNotifyServer(t)
	ctx := notifyCtx("alice")
	if _, err := s.SetNotifyChannel(ctx, &pb.SetNotifyChannelRequest{Kind: pb.NotifyChannelKind_NOTIFY_CHANNEL_KIND_NTFY, Server: "https://n.example.com", Topic: "t", Token: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteNotifyChannel(ctx, &pb.DeleteNotifyChannelRequest{}); err != nil {
		t.Fatal(err)
	}
	if rows := sec.tokenRows("alice"); len(rows) != 0 {
		t.Fatalf("token secrets left behind: %v", rows)
	}
	if _, err := s.GetNotifyChannel(ctx, &pb.GetNotifyChannelRequest{}); status.Code(err) != codes.NotFound {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestTestNotifyChannel_SendsWithToken(t *testing.T) {
	s, _, snd, _, _ := newTestNotifyServer(t)
	ctx := notifyCtx("alice")
	if _, err := s.TestNotifyChannel(ctx, &pb.TestNotifyChannelRequest{}); status.Code(err) != codes.NotFound {
		t.Fatalf("test without channel: %v", err)
	}
	if _, err := s.SetNotifyChannel(ctx, &pb.SetNotifyChannelRequest{Kind: pb.NotifyChannelKind_NOTIFY_CHANNEL_KIND_NTFY, Server: "https://n.example.com", Topic: "t", Token: "tk"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TestNotifyChannel(ctx, &pb.TestNotifyChannelRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(snd.sent) != 1 || snd.sent[0].token != "tk" || snd.sent[0].ch.Topic != "t" {
		t.Fatalf("sent = %+v", snd.sent)
	}
	snd.err = errors.New("HTTP 403")
	if _, err := s.TestNotifyChannel(ctx, &pb.TestNotifyChannelRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("failed send: %v", err)
	}
}

// writeJournal writes a run's input journal next to its record.
func writeJournal(t *testing.T, dir, run string, events ...coderun.InputEvent) {
	t.Helper()
	var b []byte
	for _, e := range events {
		line, err := e.MarshalLine()
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, line...)
	}
	if err := os.WriteFile(filepath.Join(dir, run+".input.jsonl"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}
