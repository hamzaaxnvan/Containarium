package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Wire types below mirror proto/containarium/v1/tracker.proto's messages —
// see internal/mcp/wire_format_test.go's package doc for why int64 fields
// (Number) carry a `,string` tag: grpc-gateway serializes them as JSON
// strings, and a plain int64 tag silently breaks json.Unmarshal against a
// real daemon while still passing a symmetric httptest mock.

// TrackerComment mirrors proto TrackerComment.
type TrackerComment struct {
	Author    string `json:"author"`
	CreatedAt string `json:"createdAt"`
	Body      string `json:"body"`
}

// TrackerIssue mirrors proto TrackerIssue.
type TrackerIssue struct {
	Number   int64            `json:"number,string"`
	Title    string           `json:"title"`
	Body     string           `json:"body"`
	State    string           `json:"state"`
	Labels   []string         `json:"labels,omitempty"`
	Assignee string           `json:"assignee,omitempty"`
	Comments []TrackerComment `json:"comments,omitempty"`
}

// TrackerChange mirrors proto TrackerChange.
type TrackerChange struct {
	Number    int64  `json:"number,string"`
	State     string `json:"state"`
	CiVerdict string `json:"ciVerdict"`
	Url       string `json:"url,omitempty"`
	Branch    string `json:"branch,omitempty"`
}

// GetTrackerIssueRequest's Username/Connection/Number are path-bound
// (json:"-") — they ride the URL, never the body, same convention as
// AddCollaboratorRequest.OwnerUsername.
type GetTrackerIssueRequest struct {
	Username   string `json:"-"`
	Connection string `json:"-"`
	Number     int64  `json:"-"`
}

type getTrackerIssueResponse struct {
	Issue TrackerIssue `json:"issue"`
}

// GetTrackerIssue reads a single issue, including its comments.
func (c *Client) GetTrackerIssue(req GetTrackerIssueRequest) (*TrackerIssue, error) {
	path := fmt.Sprintf("/v1/tracker/connections/%s/%s/issues/%d",
		url.PathEscape(req.Username), url.PathEscape(req.Connection), req.Number)
	respBody, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var resp getTrackerIssueResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp.Issue, nil
}

// ListTrackerIssuesRequest's filters are all optional; zero-value skips
// them. State is the CLI-style lowercase name ("open"/"closed") — this
// converts it to the wire enum name, same normalization
// ListSecuritySentryFindings applies to its own enum filters.
type ListTrackerIssuesRequest struct {
	Username   string
	Connection string
	State      string
	Labels     []string
	Search     string
}

type listTrackerIssuesResponse struct {
	Issues []TrackerIssue `json:"issues"`
}

// ListTrackerIssues enumerates issues, optionally filtered.
func (c *Client) ListTrackerIssues(req ListTrackerIssuesRequest) ([]TrackerIssue, error) {
	q := url.Values{}
	if req.State != "" {
		q.Set("state", "TRACKER_ISSUE_STATE_"+strings.ToUpper(req.State))
	}
	for _, l := range req.Labels {
		q.Add("labels", l)
	}
	if req.Search != "" {
		q.Set("search", req.Search)
	}
	path := fmt.Sprintf("/v1/tracker/connections/%s/%s/issues", url.PathEscape(req.Username), url.PathEscape(req.Connection))
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	respBody, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var resp listTrackerIssuesResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return resp.Issues, nil
}

// GetTrackerChangeRequest's fields are path-bound, same as GetTrackerIssueRequest.
type GetTrackerChangeRequest struct {
	Username   string `json:"-"`
	Connection string `json:"-"`
	Number     int64  `json:"-"`
}

type getTrackerChangeResponse struct {
	Change TrackerChange `json:"change"`
}

// GetTrackerChange reads a single change request's state and CI verdict.
func (c *Client) GetTrackerChange(req GetTrackerChangeRequest) (*TrackerChange, error) {
	path := fmt.Sprintf("/v1/tracker/connections/%s/%s/changes/%d",
		url.PathEscape(req.Username), url.PathEscape(req.Connection), req.Number)
	respBody, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var resp getTrackerChangeResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp.Change, nil
}

// CommentOnTrackerIssueRequest. Username/Connection/Number are path-bound;
// Body rides the JSON body.
type CommentOnTrackerIssueRequest struct {
	Username   string `json:"-"`
	Connection string `json:"-"`
	Number     int64  `json:"-"`
	Body       string `json:"body"`
}

type commentOnTrackerIssueResponse struct {
	Comment TrackerComment `json:"comment"`
}

// CommentOnTrackerIssue posts a stamped, sanitized comment.
func (c *Client) CommentOnTrackerIssue(req CommentOnTrackerIssueRequest) (*TrackerComment, error) {
	path := fmt.Sprintf("/v1/tracker/connections/%s/%s/issues/%d/comments",
		url.PathEscape(req.Username), url.PathEscape(req.Connection), req.Number)
	respBody, err := c.doRequest("POST", path, struct {
		Body string `json:"body"`
	}{Body: req.Body})
	if err != nil {
		return nil, err
	}
