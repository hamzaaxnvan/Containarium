package modelgateway

import (
	"sort"
	"sync"
)

// meterKey attributes usage to a tenant/skill/provider/model.
type meterKey struct {
	tenant, skill, provider, model string
}

// MeterRow is a snapshot of one attribution bucket.
type MeterRow struct {
	Tenant       string `json:"tenant"`
	Skill        string `json:"skill,omitempty"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Calls        int64  `json:"calls"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
	// BlockedCalls counts the calls above whose response the inbound scan
	// blocked (#2452). Their tokens are included in the totals: the provider
	// billed for them, so quota and spend must see them.
	BlockedCalls int64 `json:"blocked_calls,omitempty"`
}

