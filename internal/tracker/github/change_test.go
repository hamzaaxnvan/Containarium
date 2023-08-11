package github_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	trackergithub "github.com/footprintai/containarium/internal/tracker/github"
)

// TestOpenChange_DraftFlagSentAsIs proves GitHub's draft mechanic (a
// literal "draft" boolean field on the create-PR request) is exercised
// both ways — this is provider-specific behavior, deliberately outside
// the shared conformance suite, which only checks normalized output,
