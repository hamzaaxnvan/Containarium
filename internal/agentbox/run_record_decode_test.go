//go:build !windows

package agentbox

import (
	"testing"
	"time"
)

// The exported decode helpers (#2123) are the same rules listRunRecords and
// readRunRecord apply, so the daemon's box-run reader cannot drift from what
// process_list reports.
func TestDecodeRunRecord(t *testing.T) {
	cases := []struct {
		name    string
		data    string
