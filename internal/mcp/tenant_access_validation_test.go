package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantReviewMalformedSourceListsMustNeverSubmitPartialPatch(t *testing.T) {
	for _, bad := range []interface{}{"team-old", []interface{}{"team-old", 17}, []interface{}{17}} {
		posts := 0
		f := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
