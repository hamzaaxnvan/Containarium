package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NewGitHubClient returns a GitHubAPI backed by a real
// http.Client talking to api.github.com. The CLI and MCP tool
// both use this in production; tests pass a fake instead.
//
// httpClient is allowed to be nil — we'll build one with a
// sensible timeout. Pass your own if you want a custom transport
// (e.g. tracing, retry middleware).
func NewGitHubClient(httpClient *http.Client) GitHubAPI {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &githubClient{http: httpClient}
}

type githubClient struct {
	http *http.Client
}

// runnersListResponse mirrors the shape GitHub returns from
// GET /repos/{repo}/actions/runners. We only deserialize the
// fields Provision needs — typed struct, not map[string]any,
// per CLAUDE.md.
type runnersListResponse struct {
	TotalCount int `json:"total_count"`
	Runners    []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Busy   bool   `json:"busy"`
	} `json:"runners"`
}

func (c *githubClient) ListRunners(ctx context.Context, repo, pat string) ([]RegisteredRunner, error) {
	target, err := ParseTarget(repo)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://api.github.com/%s/actions/runners", target.APIPath())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
