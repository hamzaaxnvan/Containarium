package releases

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHubReleaseBaseURL is the root of GitHub's release-asset download URLs
// for this repo, shared by every caller that fetches a release binary
// directly from GitHub (#1028) — as opposed to via the sentinel's own
// fetch-release proxy, which mirrors this same layout independently
