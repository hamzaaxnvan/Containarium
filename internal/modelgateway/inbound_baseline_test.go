package modelgateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Baseline response scan (#2736, slice A of #2575): with no admin policy the
// gateway scans responses against the baseline rule table, log-only. The key
// material below is synthetic filler, never a real key.

var (
	fakeKeyHeader = "-----BEGIN " + "RSA PRIVATE KEY-----"
	fakeKeyBody   = fakeKeyHeader + "\nMIIEFAKEFAKEFAKEFAKE\n-----END " + "RSA PRIVATE KEY-----"
	fakeAWSID     = "AKIA" + "FAKEFAKEFAKEFAKE"
)

// newBaselineHarness is a gateway whose policy store answers "not
// configured": a fresh install.
func newBaselineHarness(t *testing.T, prov string, up upstreamFn) *inboundHarness {
	t.Helper()
	h := newInboundHarness(t, prov, up, nil)
	h.provider.set(nil, guardrailpolicy.ErrNotConfigured)
	return h
}

// detectionsAll waits for every request the harness served to finish on the
// gateway side, then flushes the audit queue. The baseline scan runs when the
// proxy closes the body, which can be after a client holding a full
// Content-Length body has already returned; the END lifecycle line is logged
// only once ServeHTTP is done.
func (h *inboundHarness) detectionsAll() []InboundDetection {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(h.logs.String(), " END ") < int(h.hits.Load()) {
		if time.Now().After(deadline) {
			h.t.Fatalf("gateway did not finish %d request(s)", h.hits.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.gw.FlushInboundAudit(ctx); err != nil {
		h.t.Fatalf("flush audit: %v", err)
	}
	return h.audit.allDetections()
}

// forwardedWithoutBaseline is what the gateway sends the client for the same
// upstream with the baseline off (an admin policy with no inbound rule, so
// nothing is held either). The streaming path normalises SSE framing on its
// own, so this, not the raw upstream body, is the byte-identical reference.
func forwardedWithoutBaseline(t *testing.T, prov string, up upstreamFn) string {
	t.Helper()
	ref := newInboundHarness(t, prov, up, nil)
	ref.provider.set(blockPolicy(pb.GuardrailKind_GUARDRAIL_KIND_SECRET), nil)
	resp, got := ref.call(`{"model":"m"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("reference call status = %d", resp.StatusCode)
	}
	if dets := ref.detectionsAll(); len(dets) != 0 {
		t.Fatalf("reference gateway ran the baseline: %+v", dets)
	}
	return string(got)
}

func TestBaseline_NoPolicy_SeededKeyIsFlaggedAndAudited(t *testing.T) {
	cases := []struct {
		name, prov, body string
		upstream         func(string) upstreamFn
		rule             guardrail.BaselineRuleID
	}{
		{"anthropic json pem", "anthropic", anthropicText("your key:\n" + fakeKeyBody), jsonUpstream, guardrail.BaselineRulePEMPrivateKey},
		{"openai json aws id in tool call", "openai", openAIToolCall(`{"env":"AWS_ACCESS_KEY_ID=` + fakeAWSID + `"}`), jsonUpstream, guardrail.BaselineRuleAWSAccessKeyID},
		{"gemini json pem", "gemini", geminiText(fakeKeyBody), jsonUpstream, guardrail.BaselineRulePEMPrivateKey},
		// The header is split across stream chunks; the scan reassembles it.
		{"anthropic stream pem split", "anthropic", anthropicTextStream("ok -----BEGIN RSA ", "PRIVATE KEY-----\nMIIEFAKE"), sseUpstream, guardrail.BaselineRulePEMPrivateKey},
		{"openai stream aws id split", "openai", oaiToolChunks(`{"k":"AKIA`, `FAKEFAKEFAKEFAKE"}`), sseUpstream, guardrail.BaselineRuleAWSAccessKeyID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newBaselineHarness(t, tc.prov, tc.upstream(tc.body))
			resp, got := h.call(`{"model":"m"}`)
