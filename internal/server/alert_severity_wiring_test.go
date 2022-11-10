package server

import (
	"os"
	"strings"
	"testing"
)

// A validator nothing calls is a validator that does not run. Both write
// paths have to use it: Create defaults an empty severity and then checks,
// Update checks only when the caller supplied one.
//
// Checked against the source because reaching these RPCs needs a wired store,
// and what is being asserted is the wiring itself — the same reason the
// dual_server guards in this package read source rather than standing the
// daemon up.
func TestAlertSeverityIsValidatedOnEveryWritePath(t *testing.T) {
	b, err := os.ReadFile("alert_server.go")
