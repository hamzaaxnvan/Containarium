package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #3288: FindOpenChange looks a branch's open pull request up by
// head=<owner>:<branch>, state=open.
func TestFindOpenChange(t *testing.T) {
	const branch = "containarium/issue-7"
	for _, tc := range []struct {
		name      string
		body      string
		wantFound bool
		wantNum   int64
	}{
		{"none open", `[]`, false, 0},
		{"one open", `[{"number": 5, "state": "open", "html_url": "https://github.com/acme/widgets/pull/5",
			"head": {"ref": "containarium/issue-7", "sha": "abc", "repo": {"full_name": "acme/widgets"}}}]`, true, 5},
