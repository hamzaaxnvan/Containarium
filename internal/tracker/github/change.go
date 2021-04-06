package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var _ tracker.Provider = (*Adapter)(nil)

// OpenChange opens a pull request from req.HeadBranch onto
// req.BaseBranch. req.Description is used as-is: the closing reference
// to the issue and the platform-stamped signature are the caller's
// responsibility to have already composed into it (#1923's
// SubmitTrackerChange handler), the same division of labor Comment
// already has for its own body.
func (a *Adapter) OpenChange(ctx context.Context, conn tracker.Conn, req tracker.OpenChangeRequest) (tracker.Change, error) {
	base := apiBase(conn.BaseURL)
	reqBody := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Draft bool   `json:"draft"`
	}{
		Title: req.Title,
		Body:  req.Description,
		Head:  req.HeadBranch,
		Base:  req.BaseBranch,
		Draft: req.Draft,
	}

	var pr ghPull
	if err := a.do(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/pulls", base, conn.Project),
		conn.Credential, reqBody, &pr); err != nil {
		return tracker.Change{}, err
	}
