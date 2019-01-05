package sentinel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenPolicy_Validate(t *testing.T) {
	p := NewTokenPolicy()
	p.Allow("legacy", PoolAny)
	p.Allow("prod-only", "prod")
	p.Allow("multi", "prod", "lab")
	p.Allow("unpooled-only", "")

	cases := []struct {
		name    string
		token   string
		pool    Pool
		wantErr string // substring; "" means expect success
	}{
		{name: "wildcard allows any pool", token: "legacy", pool: "prod"},
		{name: "wildcard allows empty pool", token: "legacy", pool: ""},
		{name: "single-pool token matches", token: "prod-only", pool: "prod"},
		{name: "single-pool token rejects other pool", token: "prod-only", pool: "lab", wantErr: "not authorized"},
		{name: "single-pool token rejects empty", token: "prod-only", pool: "", wantErr: "not authorized"},
		{name: "multi-pool token allows first", token: "multi", pool: "prod"},
		{name: "multi-pool token allows second", token: "multi", pool: "lab"},
		{name: "multi-pool token rejects unlisted", token: "multi", pool: "rogue", wantErr: "not authorized"},
		{name: "explicit unpooled token allows empty", token: "unpooled-only", pool: ""},
		{name: "explicit unpooled token rejects named", token: "unpooled-only", pool: "prod", wantErr: "not authorized"},
		{name: "unknown token rejected", token: "ghost", pool: "prod", wantErr: "invalid token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Validate(tc.token, tc.pool)
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestTokenPolicy_AllowReplaces(t *testing.T) {
	p := NewTokenPolicy()
	p.Allow("t", "prod")
	assert.NoError(t, p.Validate("t", "prod"))

	// Re-Allow with different pools replaces (does not merge).
	p.Allow("t", "lab")
	assert.Error(t, p.Validate("t", "prod"))
	assert.NoError(t, p.Validate("t", "lab"))
}

func TestTokenPolicy_Deny(t *testing.T) {
	p := NewTokenPolicy()
	p.Allow("t", "prod")
