package secrets

import (
	"errors"
	"strings"
	"testing"
)

// Phase 4.3 Phase A — delivery-field validation tests.
// The SQL roundtrip path lives in the integration suite
// (needs Postgres); here we cover the pure-Go validator
// + constant shape.

func TestValidateDelivery(t *testing.T) {
	cases := map[string]error{
