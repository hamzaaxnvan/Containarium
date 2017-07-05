// Package sshauthz implements the sentinel's audit-only live SSH check.
package sshauthz

import (
	"sync"
	"time"
)

// CacheKey isolates positives by backend, login and offered credential.
type CacheKey struct{ Backend, Login, Fingerprint string }

type positive struct {
	UserID           string
	checked, expires time.Time
}

// Cache retains expired positives for a bounded outage grace window. A grace
// read never refreshes checked: an outage cannot extend itself indefinitely.
type Cache struct {
	mu       sync.Mutex
	entries  map[CacheKey]positive
	now      func() time.Time
	grace    time.Duration
	capacity int
}

func NewCache(grace time.Duration, capacity int, now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	if capacity <= 0 {
		capacity = 10000
	}
	return &Cache{entries: make(map[CacheKey]positive), now: now, grace: grace, capacity: capacity}
}

func (c *Cache) prune(now time.Time) {
	for k, v := range c.entries {
		if !now.Before(v.expires) && !now.Before(v.checked.Add(c.grace)) {
			delete(c.entries, k)
		}
