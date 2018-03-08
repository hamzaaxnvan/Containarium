package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/tracker"
	trackergithub "github.com/footprintai/containarium/internal/tracker/github"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// An ambiguous upstream create (#2045): the forge accepts the POST, then
// answers only after the HTTP client has given up. The issue exists
// upstream but the daemon never learned its number. The RPC must audit
// the attempt as outcome-unknown, return a distinct error that tells the
// agent not to retry blindly, and keep the attempt counted against
// max_children_per_run so a retry cannot file past the cap.

// slowForge is a fake GitHub that records every accepted create and, for
// the first one only, holds the response until the client gives up.
type slowForge struct {
	srv     *httptest.Server
	creates atomic.Int32
}

func newSlowForge(t *testing.T) *slowForge {
	t.Helper()
	f := &slowForge{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/issues") {
			http.NotFound(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body) // read the whole create...
		n := f.creates.Add(1)              // ...and file it
		if n == 1 {
			select { // ...but answer too late (the body is drained, so
			// the server notices the client hanging up)
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"number":%d,"title":"t","state":"open"}`, 600+n)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// slowForgeProvider sends CreateIssue through the real GitHub adapter to
// the slow forge, with a short client timeout standing in for the
// production one; every other verb stays on the in-memory fake.
type slowForgeProvider struct {
	*fakeWriterProvider
	gh   *trackergithub.Adapter
	base string
}

func (p *slowForgeProvider) CreateIssue(ctx context.Context, conn tracker.Conn, n tracker.NewIssue) (tracker.Issue, error) {
	conn.BaseURL = p.base
	return p.gh.CreateIssue(ctx, conn, n)
}

func setUpSlowForge(t *testing.T, user string, policy *pb.TrackerPolicy) (*ContainerServer, *slowForge, context.Context, context.Context) {
	t.Helper()
	fake := &fakeWriterProvider{}
	s, runCtx, operatorCtx := setUpCreateConnection(t, user, fake, policy)
	s.auditStore = mustTestAuditStore(t)
	forge := newSlowForge(t)
	s.trackerDescribers = fakeDescriberSet(&slowForgeProvider{
		fakeWriterProvider: fake,
		gh:                 trackergithub.New(&http.Client{Timeout: 200 * time.Millisecond}),
		base:               forge.srv.URL,
	})

	pool, err := pgxpool.New(context.Background(), os.Getenv("CONTAINARIUM_TEST_DSN"))
	if err != nil {
