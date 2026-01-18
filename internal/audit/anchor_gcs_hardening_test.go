package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// #2912 "should fix" items from the post-merge review of #2882.

// gcloud expands * ? [ in a gs:// URL, so a wildcard prefix would make
// LastPublished list something else and silently report "never anchored".
func TestNewGCSAnchorSink_RejectsWildcardsAndOddNames(t *testing.T) {
	g := newFakeGcloud()
	for _, uri := range []string{
		"gs://example-bucket/audit-*",
