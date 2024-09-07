package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestNativeTenantCLIProcessHelper(t *testing.T) {
	mode := os.Getenv("NATIVE_TENANT_CLI_FIXTURE")
	if mode == "" {
		return
	}
	rootCmd.SetArgs([]string{"create", "workload", "--network-tenant", "team-a", "--tenant", "default", "--no-ssh-key", "--force", "--server", os.Getenv("NATIVE_TENANT_CLI_URL"), "--http", "--token", "fixture-token"})
	err := Execute()
