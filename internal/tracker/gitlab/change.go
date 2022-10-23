package gitlab

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

// draftTitlePrefix marks a merge request as draft/WIP. GitLab has no
// dedicated boolean field for this on the create-MR endpoint (unlike
// GitHub's `draft`); the documented, UI-recognized mechanism is this
// title prefix.
const draftTitlePrefix = "Draft: "

// OpenChange opens a merge request from req.HeadBranch onto
// req.BaseBranch. req.Description is used as-is — the closing
// reference and platform-stamped signature are the caller's
// responsibility to have already composed into it.
func (a *Adapter) OpenChange(ctx context.Context, conn tracker.Conn, req tracker.OpenChangeRequest) (tracker.Change, error) {
	apiBase := apiBaseURL(conn.BaseURL)
	projectPath := url.PathEscape(conn.Project)
