package container

import "testing"

// TestUserdelBusy pins the one userdel failure the delete-cascade retries
// instead of surfacing (#1035): a live SSH session to the box that was just
// deleted. Everything else must stay a hard error — silently swallowing, say,
// a permission failure would leave an orphaned account with no log trail.
func TestUserdelBusy(t *testing.T) {
	cases := []struct {
