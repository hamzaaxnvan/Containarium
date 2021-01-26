package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// webhookConfig is a fixed WebhookConfigSource: the URL, no secret.
type webhookConfig string

func (c webhookConfig) Get(_ context.Context, key string) (string, error) {
	if key == "alert_webhook_url" {
		return string(c), nil
	}
	return "", nil
}

// webhookReceiver collects the finding webhook's posts, decoded the way a
// consumer reads them (encoding/json over the proto, enums as numbers).
type webhookReceiver struct {
	mu    sync.Mutex
	posts []*pb.Finding
}

func (rx *webhookReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var f pb.Finding
	if err := json.Unmarshal(body, &f); err == nil {
		rx.mu.Lock()
		rx.posts = append(rx.posts, &f)
		rx.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

// wait returns the posts once there are n of them, then checks that no
// more arrive for a short while.
func (rx *webhookReceiver) wait(t *testing.T, n int) []*pb.Finding {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rx.mu.Lock()
		got := len(rx.posts)
		rx.mu.Unlock()
		if got >= n || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	rx.mu.Lock()
	defer rx.mu.Unlock()
	if len(rx.posts) != n {
		t.Fatalf("webhook got %d posts, want %d: %s", len(rx.posts), n, describePosts(rx.posts))
	}
	return append([]*pb.Finding(nil), rx.posts...)
}

func describePosts(posts []*pb.Finding) string {
	var b strings.Builder
	for _, p := range posts {
		b.WriteString("\n  state=" + p.GetState().String() + " container=" + p.GetContainer() +
			" response=" + p.GetResponse().GetState().String() + "@" + p.GetResponse().GetContainer())
	}
	return b.String()
}

// withWebhook runs the real WebhookNotifier (gate and HTTP worker) behind
// the fixture's finding store, posting to a local receiver.
func withWebhook(t *testing.T, fx *responderFixture) *webhookReceiver {
	t.Helper()
	rx := &webhookReceiver{}
	srv := httptest.NewServer(rx)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	n := threatdetect.NewWebhookNotifier(webhookConfig(srv.URL), nil)
	n.Start(ctx)
	fx.findings.notifier = n
	return rx
}

type wantPost struct {
	what      string
	findingSt pb.FindingState
	box       string           // the finding's container and response.container
	response  pb.ResponseState // UNSPECIFIED: no response
}

func checkPosts(t *testing.T, posts []*pb.Finding, want []wantPost) {
	t.Helper()
	for i, w := range want {
		p := posts[i]
		r := p.GetResponse()
		if p.GetState() != w.findingSt || p.GetContainer() != w.box || r.GetState() != w.response ||
			(w.response != pb.ResponseState_RESPONSE_STATE_UNSPECIFIED && r.GetContainer() != w.box) {
			t.Errorf("post %d (%s): finding %s container=%q response %s@%q; want finding %s, %s on %q",
				i+1, w.what, p.GetState(), p.GetContainer(), r.GetState(), r.GetContainer(), w.findingSt, w.response, w.box)
		}
	}
}

// TestSharedFinding_WebhookDeliversEachBox is the #2820 repro through the
// responder and the real webhook gate: two boxes of one tenant share a
// miner-signature finding under THROTTLE; box A is released, then box B.
// Every apply and release is delivered naming its box, B's release (the
// last) carries the resolve, and between the two releases the finding (its
// API/CLI JSON as well) shows B's APPLIED record, never RELEASED.
func TestSharedFinding_WebhookDeliversEachBox(t *testing.T) {
	ctx := context.Background()
	fx, sink := queuedThrottle(t)
	rx := withWebhook(t, fx)

	fx.r.runJob(ctx, queueFor(t, fx, sink, "box-a"))
	fx.r.runJob(ctx, queueFor(t, fx, sink, "box-b"))
	f, kept, err := fx.r.Release(ctx, "box-a")
	if err != nil {
		t.Fatal(err)
	}
	if !kept || f == nil || f.State != threatdetect.FindingStateOpen {
		t.Fatalf("Release(box-a) = kept=%t finding %+v; want kept open", kept, f)
	}
	if row, err := openAction(fx, "box-b"); err != nil || row.Action != pb.ProcessAction_PROCESS_ACTION_THROTTLE {
		t.Fatalf("box-b's throttle after box-a's release: %+v, %v", row, err)
	}
	// What ReleaseProcessAction returns and what `findings list` reads.
	for what, got := range map[string]*threatdetect.Finding{"Release's finding": f, "stored finding": mustGet(t, fx, f.ID)} {
		r := got.ToProto().GetResponse()
