//go:build gitlab_e2e

// Package gitlab_test's e2e suite is the design note's opt-in lane: "a real
// GitLab CE container proving quick-action sanitization and single-assignee
// behavior end to end". It runs against a REAL GitLab instance (env below),
// never a fixture, because both properties it proves are exactly the ones a
// recorded fixture cannot: whether GitLab's own parser executes a quick
// action, and how GitLab's own single-assignee model actually reacts to a
// second assignment attempt.
//
// Env (all required — a missing one fails the suite, per this repo's
// "a skipped test here is indistinguishable from a passing one" convention;
// see store-integration.yml):
//
//	GITLAB_E2E_URL    site root, e.g. http://localhost:8929
//	GITLAB_E2E_TOKEN  a personal access token with the `api` scope
//
// The suite creates and deletes its own project per run (TestMain), so it
// is safe to re-run against the same instance.
//
// Run it locally:
//
//	docker run -d --name gitlab-ce --hostname gitlab.local \
//	  -e GITLAB_OMNIBUS_CONFIG='external_url "http://gitlab.local"; puma["worker_processes"] = 0; sidekiq["max_concurrency"] = 5; prometheus_monitoring["enable"] = false;' \
//	  -p 8929:80 --shm-size 256m gitlab/gitlab-ce:latest
//	# wait for: docker exec gitlab-ce curl -s http://localhost/-/readiness -> 200 (~1-2 min)
//	docker exec gitlab-ce gitlab-rails runner "
//	  t = User.find_by_username('root').personal_access_tokens.create(scopes: ['api'], name: 'e2e', expires_at: 1.day.from_now)
//	  t.set_token('local-e2e-token'); t.save!"
//	GITLAB_E2E_URL=http://localhost:8929 GITLAB_E2E_TOKEN=local-e2e-token \
//	  go test -tags gitlab_e2e -v ./internal/tracker/gitlab/... -run TestGitLabE2E
//	docker rm -f gitlab-ce
package gitlab_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/tracker"
	trackergitlab "github.com/footprintai/containarium/internal/tracker/gitlab"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var (
	e2eBaseURL    string
	e2eToken      string
	e2eProject    string // "namespace/path", set by TestMain
	e2eProjectID  int
	e2eHTTPClient = &http.Client{Timeout: 30 * time.Second}
)

func e2eRequire(t testing.TB, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Fatalf("gitlab e2e: %s is required (this suite fails rather than skips on a missing env — see package doc)", name)
	}
	return v
}

// apiCall is a raw, adapter-independent GitLab v4 call used to set up
// fixtures and to observe ground truth (did GitLab actually execute a quick
// action, is this issue actually assigned). Using the adapter under test to
// verify the adapter under test would prove nothing. Returns an error
// instead of failing a *testing.T directly, so it works from TestMain (which
// has none) as well as from within a test via e2eAPI below.
func apiCall(method, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, e2eBaseURL+"/api/v4"+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", e2eToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e2eHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, raw)
	}
	if len(raw) == 0 {
