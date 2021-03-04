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
