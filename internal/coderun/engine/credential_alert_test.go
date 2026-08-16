package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialAlertStatusScript(t *testing.T) {
	const now = int64(1800000000)
	script, ok := CredentialAlertStatusScript(NameClaude)
	if !ok {
		t.Fatal("missing Claude alert probe")
	}
	for _, tc := range []struct {
		name                            string
		expires                         int64
