package zfskey

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestCache returns a cache on a controllable clock so TTL behaviour
// is tested by advancing time rather than by sleeping.
func newTestCache(ttl time.Duration) (*Cache, func(time.Duration)) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	c := NewCache(ttl)
	c.now = func() time.Time { return now }
	return c, func(d time.Duration) { now = now.Add(d) }
}

func TestCacheStoresAndReturnsKeys(t *testing.T) {
	c, _ := newTestCache(time.Minute)
	k := Key{b: bytes.Repeat([]byte{7}, KeyLen)}

	if _, ok := c.Get("alice"); ok {
		t.Fatal("empty cache reported a hit")
	}

	c.Put("alice", k)
	got, ok := c.Get("alice")
	if !ok {
		t.Fatal("cached key not returned")
	}
	if !bytes.Equal(got.Bytes(), k.Bytes()) {
		t.Error("cache returned different material than it was given")
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
}

