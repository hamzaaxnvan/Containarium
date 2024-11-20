//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// #3130: the daemon bound its HTTP port only after waiting up to two minutes
// for the core containers and two more for Postgres, so for ~4 minutes the
// backend looked dead. It now binds first and reports "degraded" with the
// reason while it waits.

type startupHealthBody struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func fetchStartupHealth(t *testing.T, addr string) startupHealthBody {
	t.Helper()
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health status code = %d, want 200 (degraded is live, not down)", resp.StatusCode)
	}
	var b startupHealthBody
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		t.Fatalf("decode /health: %v", err)
	}
	return b
}

func waitForReason(t *testing.T, addr, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		got := fetchStartupHealth(t, addr)
		if got.Status == "degraded" && got.Reason == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("/health = %+v, want degraded with reason %q within %s", got, want, within)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// unreachablePostgresDSN points at a closed local port, so every ping fails
// fast with "connection refused".
func unreachablePostgresDSN(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return "postgres://u:p@127.0.0.1:" + strconv.Itoa(port) + "/db?connect_timeout=1&sslmode=disable"
}

// Acceptance test: with an unreachable Postgres and no core containers, the
// health endpoint answers within a few seconds with the degraded state.
func TestStartupHealth_DegradedWhileCoreContainersAndPostgresWaitsRun(t *testing.T) {
	sp := bindStartupHealth(true, "127.0.0.1", 0)
	if sp.listener == nil {
		t.Fatal("bindStartupHealth did not bind the HTTP port")
	}
	t.Cleanup(sp.Close)
	addr := sp.listener.Addr().String()

	// "No core containers": the core-container wait blocks until released.
	releaseCore := make(chan struct{})
	coreDone := make(chan struct{})
	go func() {
		sp.SetReason(startupReasonCoreContainers)
		<-releaseCore
		close(coreDone)
	}()

