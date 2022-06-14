package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI's exit status is the workflow's verdict: 1 is a policy failure,
// 2 is "no verdict", and the two must never be confused with a pass.
func TestDiffExitCodes(t *testing.T) {
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer broken.Close()
