package sentinel

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPrimaryRegistry_RegisterLookupHeartbeat(t *testing.T) {
	r := NewPrimaryRegistry()

	// Initial registration
	stored := r.Register(Primary{Pool: "prod", Hostname: "prod.example.com", IP: "10.0.0.10", Port: 443})
	assert.NotNil(t, stored)
	assert.Equal(t, Pool("prod"), stored.Pool)
	assert.False(t, stored.RegisteredAt.IsZero())
	assert.Equal(t, stored.RegisteredAt, stored.LastHeartbeat)

	originalRegisteredAt := stored.RegisteredAt
	time.Sleep(10 * time.Millisecond)

	// Re-register: RegisteredAt preserved, LastHeartbeat advanced
	updated := r.Register(Primary{Pool: "prod", Hostname: "prod.example.com", IP: "10.0.0.11", Port: 443})
	assert.Equal(t, originalRegisteredAt, updated.RegisteredAt)
	assert.True(t, updated.LastHeartbeat.After(originalRegisteredAt))
	assert.Equal(t, "10.0.0.11", updated.IP, "IP should update on re-register")

	// Lookup hits
	assert.NotNil(t, r.LookupByPool("prod"))
	assert.NotNil(t, r.LookupByHostname("prod.example.com"))

	// Heartbeat refreshes
	prevHB := r.LookupByPool("prod").LastHeartbeat
	time.Sleep(10 * time.Millisecond)
	hb := r.Heartbeat("prod")
	assert.NotNil(t, hb)
	assert.True(t, hb.LastHeartbeat.After(prevHB))

	// Heartbeat for unknown pool returns nil
	assert.Nil(t, r.Heartbeat("ghost"))

	// Unregister
	assert.True(t, r.Unregister("prod"))
	assert.False(t, r.Unregister("prod"), "second unregister should be no-op")
	assert.Nil(t, r.LookupByPool("prod"))
}

func TestPrimaryRegistry_LookupByHostnameMatchesAliases(t *testing.T) {
	r := NewPrimaryRegistry()
	r.Register(Primary{
		Pool:     "prod",
		Hostname: "prod.example.com",
		Aliases:  []string{"api.example.com", "voice.example.com"},
		IP:       "10.0.0.10",
		Port:     443,
	})
	r.Register(Primary{
		Pool:     "lab",
		Hostname: "lab.example.com",
		Aliases:  []string{"lab-api.example.com"},
		IP:       "10.0.0.20",
		Port:     443,
	})

	// Primary hostname matches
	p := r.LookupByHostname("prod.example.com")
	if assert.NotNil(t, p) {
		assert.Equal(t, Pool("prod"), p.Pool)
	}

	// Alias matches
	p = r.LookupByHostname("api.example.com")
	if assert.NotNil(t, p) {
		assert.Equal(t, Pool("prod"), p.Pool, "api.example.com should resolve to prod via alias")
	}

	// Different pool's alias resolves to that pool
	p = r.LookupByHostname("lab-api.example.com")
	if assert.NotNil(t, p) {
		assert.Equal(t, Pool("lab"), p.Pool)
	}

	// Unknown hostname returns nil
	assert.Nil(t, r.LookupByHostname("ghost.example.com"))

