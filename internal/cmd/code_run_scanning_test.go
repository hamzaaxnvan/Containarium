package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func scanningEngine(t *testing.T, cred engine.CredentialSource) engine.Engine {
	t.Helper()
	eng, err := engine.For(engine.NameClaude, engine.Options{Credential: cred})
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

// storeStatusAPI answers GetGuardrailStatus the way the daemon does: from the
// policy store through EffectiveStatus.
type storeStatusAPI struct{ fake *fakeGuardrailPolicyAPI }

