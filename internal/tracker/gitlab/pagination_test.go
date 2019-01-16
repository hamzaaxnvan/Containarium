package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
)

// TestListIssues_StopsAtMaxListPages proves the pagination loop (#2040)
// is bounded: an upstream that always answers with a next link is read
