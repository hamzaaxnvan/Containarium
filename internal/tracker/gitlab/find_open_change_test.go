package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #3288: FindOpenChange looks a branch's open merge request up by
// source_branch, state=opened, and ignores one opened from a fork's
// same-named branch.
func TestFindOpenChange(t *testing.T) {
	const branch = "containarium/issue-7"
	for _, tc := range []struct {
		name      string
