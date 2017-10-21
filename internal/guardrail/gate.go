package guardrail

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// GateResult is the gate's decision over a re-scan of the redacted output.
type GateResult struct {
	Verdict pb.GuardrailVerdict
	// Residual findings per kind that the gate actually judged — findings
	// that fall inside a placeholder are excluded (see Gate).
	Residual map[pb.GuardrailKind]int64
	// Findings the engine reported inside a placeholder token: an engine
	// reading "[[PERSON_NAME:…]]" as a name. Counted for transparency,
	// never held against the subject.
	InPlaceholder int64
	Gaps          int64
	// Reasons is empty on PASS and names every rule that failed otherwise.
	Reasons []string
}

// Gate decides from the re-scan. It fails on any coverage gap (an engine
// that did not look is not a clean bill), on any BLOCK finding recorded at
// redaction, and on residual findings beyond a rule's max_residual.
//
// A finding whose span lies entirely within a placeholder token in the
// wiped text is ignored: the spike that preceded this package found that
// the only residuals left after a good redaction were the detector reading
// the placeholder itself, and a gate that can never reach PASS is not a
// gate. The placeholder spans are recomputed from the wiped text here, not
// trusted from the redaction step, so the gate stands on its own.
func Gate(rescan *pb.GuardrailScanResponse, wiped map[string]string, policy *pb.GuardrailPolicy, blocked map[pb.GuardrailKind]int64) *GateResult {
	res := &GateResult{
		Verdict:  pb.GuardrailVerdict_GUARDRAIL_VERDICT_PASS,
		Residual: map[pb.GuardrailKind]int64{},
		Gaps:     int64(len(rescan.GetGaps())),
	}
	holes := map[string][][2]int{}
	for id, text := range wiped {
		holes[id] = placeholderSpans(text)
	}
	for _, f := range rescan.GetFindings() {
		if insideAny(holes[f.GetUnitId()], int(f.GetStart()), int(f.GetEnd())) {
			res.InPlaceholder++
			continue
		}
		res.Residual[f.GetKind()]++
	}

	fail := func(format string, a ...any) {
		res.Verdict = pb.GuardrailVerdict_GUARDRAIL_VERDICT_FAIL
