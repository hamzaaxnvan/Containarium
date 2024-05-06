package client

import "testing"

// The gateway renders the proto int64 count as a quoted decimal string, so a
// plain int64 field would fail `containarium token unscoped-report` the same
// way #3250 broke `containarium info`.
func TestGetUnscopedTokenReportDecodesQuotedInt64(t *testing.T) {
