package sentinel

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRenderMetrics_Format(t *testing.T) {
	m := NewManager(Config{}, &fakeRecoveryProvider{})
	m.preemptCount = 3
	m.recoveredCount = 2
	out := m.renderMetrics(0 /*down*/, 42)

	for _, want := range []string{
		"# TYPE sentinel_preempted_total counter",
		"sentinel_preempted_total 3",
		"sentinel_recovered_total 2",
		"sentinel_state 0",
		"sentinel_outage_seconds 42",
	} {
