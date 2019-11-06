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
	boom := errors.New("incus unavailable")
	tests := []struct {
		name       string
		asRunUser  bool
		level      string
		levelErr   error
		exists     bool
		existsErr  error
		wantUser   string
		wantStatus int // 0 = no refusal
	}{
		{name: "flag off keeps the login user", wantUser: "alice"},
		{name: "flag off ignores a recorded level", level: "none", exists: true, wantUser: "alice"},
		{name: "recorded level and run user opens as the run user", asRunUser: true, level: "build_tools", exists: true, wantUser: toolaccess.RunUser},
		{name: "none is a recorded level", asRunUser: true, level: "none", exists: true, wantUser: toolaccess.RunUser},
