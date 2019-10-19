package alert

import (
	"strings"
	"testing"
)

// The severity is written into the generated Prometheus rule as a label, and
// Alertmanager routes on that label. An unrecognised value produces a rule
// that fires and matches no route — the alert reaches nobody, and nothing
// reports an error. Silence is the same observable outcome as "nothing is
// wrong", which is why this is worth refusing at the API rather than
// discovering during an incident.
func TestValidateSeverity(t *testing.T) {
	for _, ok := range []string{SeverityCritical, SeverityWarning, SeverityInfo} {
		if err := ValidateSeverity(ok); err != nil {
			t.Errorf("ValidateSeverity(%q) rejected a documented severity: %v", ok, err)
		}
	}

	for _, bad := range []string{
		"",          // an explicitly empty severity is not the same as unset
		"warnning",  // the typo this exists for
		"Warning",   // case matters to Alertmanager's matcher
