package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
)

func TestDescribeCredential_ClassicPAT_ReportsScopesAndBroadBreadth(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("X-OAuth-Scopes", "repo, read:org")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	defer srv.Close()

	a := New(nil)
	info, err := a.DescribeCredential(context.Background(), tracker.Conn{
		BaseURL:    srv.URL,
		Credential: "ghp_classictoken",
	})
	if err != nil {
		t.Fatalf("DescribeCredential: %v", err)
	}
	if gotPath != "/user" {
		t.Errorf("path = %q, want /user", gotPath)
	}
	if gotAuth != "token ghp_classictoken" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "token ghp_classictoken")
	}
	if len(info.Scopes) != 2 || info.Scopes[0] != "repo" || info.Scopes[1] != "read:org" {
		t.Errorf("Scopes = %v, want [repo read:org]", info.Scopes)
	}
	if info.Breadth != tracker.BreadthBroad {
		t.Errorf("Breadth = %v, want BreadthBroad", info.Breadth)
	}
	if !info.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt = %v, want zero (GitHub never reports PAT expiry via this endpoint)", info.ExpiresAt)
	}
}

func TestDescribeCredential_FineGrainedPAT_ReportsPreferredBreadth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fine-grained PATs don't set X-OAuth-Scopes.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	defer srv.Close()

	a := New(nil)
	info, err := a.DescribeCredential(context.Background(), tracker.Conn{
		BaseURL:    srv.URL,
		Credential: "github_pat_finegrained",
	})
	if err != nil {
		t.Fatalf("DescribeCredential: %v", err)
	}
	if info.Breadth != tracker.BreadthPreferred {
		t.Errorf("Breadth = %v, want BreadthPreferred", info.Breadth)
	}
	if info.Scopes != nil {
		t.Errorf("Scopes = %v, want nil", info.Scopes)
	}
}

func TestDescribeCredential_InstallationToken_ProbesRateLimit(t *testing.T) {
