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
