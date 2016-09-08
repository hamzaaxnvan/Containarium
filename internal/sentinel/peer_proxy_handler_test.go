package sentinel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// #1102: /peer/ was the one route on the binary-server mux with no
// authentication, while every neighbouring /sentinel/* route is HMAC
// gated. These tests pin both halves of the fix — the gate itself, and
// the uniform rejection that stops an authorized caller from using the
// route to enumerate which backends a sentinel fronts.

const testAdminSecret = "peer-proxy-admin-secret-32-bytes-long!"

// newPeerProxyTestServer serves the REAL binary-server route table over
// a manager holding one backend that points at a stub daemon.
//
// It deliberately calls buildBinaryServerMux rather than registering
// /peer/ itself. An earlier version of this file built its own mux, and
// mutation testing showed the consequence: removing the HMAC middleware
// from binaryserver.go entirely — i.e. reverting the whole fix — left
// every test in this file green, because they were exercising a route
// table the test had wired correctly by hand. Which middleware a route
// is registered behind IS the fix for #1102, so it has to be the real
// registration under test.
func newPeerProxyTestServer(t *testing.T, adminSecret string) (*httptest.Server, string) {
	t.Helper()

	// Stub daemon standing in for the backend's health-port listener.
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "daemon saw "+r.URL.Path)
	}))
	t.Cleanup(daemon.Close)

	du, err := url.Parse(daemon.URL)
	if err != nil {
		t.Fatalf("parse daemon url: %v", err)
	}
	port, err := strconv.Atoi(du.Port())
	if err != nil {
		t.Fatalf("daemon port: %v", err)
	}

	m := &Manager{
		backends:    NewBackendPool(),
		adminSecret: []byte(adminSecret),
	}
	m.config.HealthPort = port
