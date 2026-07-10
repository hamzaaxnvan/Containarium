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
