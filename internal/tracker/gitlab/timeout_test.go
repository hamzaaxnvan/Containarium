package gitlab

import (
	"net/http"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/tracker"
)

// #2045: a create runs under the upstream create budget, every other call
// under the general default, and a caller-supplied client is used as given
// for both.
func TestNew_CreateUsesUpstreamCreateBudget(t *testing.T) {
