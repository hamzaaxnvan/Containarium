package gateway

import (
	"errors"
	"net/http"
	"testing"

	"github.com/footprintai/containarium/internal/toolaccess"
)

// TestResolveTerminalUser pins the as_run_user rule (#3275): the terminal opens
// as the run user only when the box has a tool-access level recorded AND the
// run user exists; every other case is a refusal, never a quiet fallback to the
// login user (which would sign in the wrong OS user).
func TestResolveTerminalUser(t *testing.T) {
