package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/tracker"
)

// gitlabFixtureServer wires /api/v4/personal_access_tokens/self and
// /api/v4/user handlers, since DescribeCredential always calls the
// first and best-effort calls the second.
func gitlabFixtureServer(t *testing.T, selfBody string, selfStatus int, userBody string, userStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/personal_access_tokens/self":
			w.WriteHeader(selfStatus)
			_, _ = w.Write([]byte(selfBody))
		case "/api/v4/user":
			w.WriteHeader(userStatus)
			_, _ = w.Write([]byte(userBody))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
}

func TestDescribeCredential_PersonalToken_BroadBreadth(t *testing.T) {
	srv := gitlabFixtureServer(t,
		`{"active":true,"revoked":false,"scopes":["api","read_repository"],"expires_at":"2027-06-15"}`, http.StatusOK,
		`{"username":"alice","bot":false}`, http.StatusOK,
	)
	defer srv.Close()

	a := New(nil)
	info, err := a.DescribeCredential(context.Background(), tracker.Conn{
		BaseURL:    srv.URL,
		Credential: "glpat-personal",
	})
	if err != nil {
		t.Fatalf("DescribeCredential: %v", err)
	}
	if len(info.Scopes) != 2 || info.Scopes[0] != "api" || info.Scopes[1] != "read_repository" {
		t.Errorf("Scopes = %v, want [api read_repository]", info.Scopes)
	}
	want := time.Date(2027, 6, 15, 0, 0, 0, 0, time.UTC)
	if !info.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", info.ExpiresAt, want)
	}
	if info.Breadth != tracker.BreadthBroad {
		t.Errorf("Breadth = %v, want BreadthBroad (personal account)", info.Breadth)
	}
}

func TestDescribeCredential_ProjectAccessToken_PreferredBreadth(t *testing.T) {
	srv := gitlabFixtureServer(t,
		`{"active":true,"revoked":false,"scopes":["read_api"],"expires_at":"2027-01-01"}`, http.StatusOK,
		`{"username":"project_123_bot_abc123","bot":true}`, http.StatusOK,
	)
	defer srv.Close()

	a := New(nil)
	info, err := a.DescribeCredential(context.Background(), tracker.Conn{
		BaseURL:    srv.URL,
		Credential: "glpat-project",
	})
	if err != nil {
		t.Fatalf("DescribeCredential: %v", err)
	}
	if info.Breadth != tracker.BreadthPreferred {
		t.Errorf("Breadth = %v, want BreadthPreferred (project access token)", info.Breadth)
	}
}

func TestDescribeCredential_GroupAccessToken_BroadBreadth(t *testing.T) {
	srv := gitlabFixtureServer(t,
		`{"active":true,"revoked":false,"scopes":["api"],"expires_at":"2027-01-01"}`, http.StatusOK,
		`{"username":"group_456_bot_xyz789","bot":true}`, http.StatusOK,
	)
	defer srv.Close()

	a := New(nil)
	info, err := a.DescribeCredential(context.Background(), tracker.Conn{
		BaseURL:    srv.URL,
		Credential: "glpat-group",
	})
	if err != nil {
		t.Fatalf("DescribeCredential: %v", err)
	}
	if info.Breadth != tracker.BreadthBroad {
		t.Errorf("Breadth = %v, want BreadthBroad (group access token is wider than project)", info.Breadth)
	}
}

func TestDescribeCredential_RevokedToken_ReturnsErrCredentialInvalid(t *testing.T) {
	srv := gitlabFixtureServer(t,
		`{"active":true,"revoked":true,"scopes":["api"]}`, http.StatusOK,
		``, http.StatusOK,
	)
	defer srv.Close()

	a := New(nil)
	_, err := a.DescribeCredential(context.Background(), tracker.Conn{
		BaseURL:    srv.URL,
		Credential: "glpat-revoked",
	})
	if !errors.Is(err, tracker.ErrCredentialInvalid) {
		t.Fatalf("err = %v, want ErrCredentialInvalid", err)
	}
}

func TestDescribeCredential_InactiveToken_ReturnsErrCredentialInvalid(t *testing.T) {
	srv := gitlabFixtureServer(t,
		`{"active":false,"revoked":false,"scopes":["api"]}`, http.StatusOK,
		``, http.StatusOK,
	)
	defer srv.Close()

	a := New(nil)
	_, err := a.DescribeCredential(context.Background(), tracker.Conn{
		BaseURL:    srv.URL,
		Credential: "glpat-inactive",
	})
	if !errors.Is(err, tracker.ErrCredentialInvalid) {
		t.Fatalf("err = %v, want ErrCredentialInvalid", err)
	}
}

func TestDescribeCredential_Unauthorized_ReturnsErrCredentialInvalid(t *testing.T) {
