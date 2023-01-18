package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestSecretEnvFlag_UsageNeverPrintsTheValue pins #2595: a secret taken from
// the environment must still be the flag's value, but cobra's usage and
// --help must not render it as the flag's default.
func TestSecretEnvFlag_UsageNeverPrintsTheValue(t *testing.T) {
	const secret = "eyJhbGciOiJIUzI1NiJ9.super-secret-payload.sig"
	t.Setenv("CONTAINARIUM_TEST_SECRET", secret)

	var got string
	c := &cobra.Command{Use: "probe"}
	secretEnvFlag(c.Flags(), &got, "token", "CONTAINARIUM_TEST_SECRET", "a token")

	if got != secret {
		t.Fatalf("flag value = %q, want the env value (it must still apply)", got)
	}
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	_ = c.Usage()
	_ = c.Help()
