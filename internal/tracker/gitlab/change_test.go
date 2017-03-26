package gitlab_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	trackergitlab "github.com/footprintai/containarium/internal/tracker/gitlab"
)

// TestOpenChange_DraftTitlePrefix proves GitLab's draft mechanic (a
// "Draft: " title prefix — GitLab has no dedicated boolean field on
// the create-MR endpoint) both fires when requested and does NOT
// double up if a caller's title already carries the prefix.
// Provider-specific, deliberately outside the shared conformance
// suite.
