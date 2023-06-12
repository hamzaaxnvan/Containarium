package sentinel

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestBYOCRouteRegistry_ReplaceAllAndLookup(t *testing.T) {
	r := NewBYOCRouteRegistry()

	if _, ok := r.Lookup("nope.containarium.dev"); ok {
		t.Fatal("empty registry should not resolve anything")
	}

	r.ReplaceAll([]BYOCRoute{
		{Hostname: "a-acme.containarium.dev", BackendID: "tunnel-h1", Port: 8080},
		{Hostname: "b-acme.containarium.dev", BackendID: "tunnel-h2", Port: 3000},
		// malformed entries must be dropped, not stored:
		{Hostname: "", BackendID: "tunnel-h3", Port: 8080},
		{Hostname: "c.containarium.dev", BackendID: "", Port: 8080},
		{Hostname: "d.containarium.dev", BackendID: "tunnel-h4", Port: 0},
	})

	got, ok := r.Lookup("a-acme.containarium.dev")
	if !ok || got.BackendID != "tunnel-h1" || got.Port != 8080 {
		t.Fatalf("lookup a = %+v ok=%v", got, ok)
	}
	if _, ok := r.Lookup("c.containarium.dev"); ok {
