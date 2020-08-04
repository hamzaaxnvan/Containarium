package integration

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tfembed "github.com/footprintai/containarium/terraform/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2ERebootPersistenceTerraform is an E2E test using Terraform for infrastructure
func TestE2ERebootPersistenceTerraform(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Check required environment variables
	projectID := os.Getenv("GCP_PROJECT")
	if projectID == "" {
		t.Skip("GCP_PROJECT not set, skipping E2E test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	// Setup test workspace
	workspace := setupTerraformWorkspace(t)
	defer cleanupTerraformWorkspace(t, workspace)

	t.Logf("Starting E2E reboot persistence test with Terraform...")
	t.Logf("  Project: %s", projectID)
	t.Logf("  Workspace: %s", workspace)

	var instanceIP string
	var instanceName string
	var zone string

	// Cleanup function
	defer func() {
		if !t.Failed() || os.Getenv("KEEP_INSTANCE") != "true" {
			t.Log("Destroying infrastructure...")
			destroyTerraform(t, ctx, workspace)
		} else {
			t.Logf("Test failed - keeping infrastructure for debugging")
			t.Logf("To cleanup: cd %s && terraform destroy -auto-approve", workspace)
		}
	}()

	// Step 1: Deploy infrastructure with Terraform
	t.Run("DeployInfrastructure", func(t *testing.T) {
		deployTerraform(t, ctx, workspace, projectID)
	})

	// Step 2: Get instance details from Terraform outputs
	t.Run("GetInstanceInfo", func(t *testing.T) {
		var err error
		instanceIP, instanceName, zone, err = getTerraformOutputs(t, ctx, workspace)
		require.NoError(t, err)
		require.NotEmpty(t, instanceIP, "Instance IP should not be empty")
		require.NotEmpty(t, instanceName, "Instance name should not be empty")
		require.NotEmpty(t, zone, "Zone should not be empty")
		t.Logf("Instance: %s at IP: %s in zone: %s", instanceName, instanceIP, zone)
	})

	// Step 3: Wait for instance to be ready
	t.Run("WaitForInstanceReady", func(t *testing.T) {
		waitForInstanceReadyTF(t, ctx, instanceName, zone)
	})

	// Step 4: Verify ZFS setup
	t.Run("VerifyZFSSetup", func(t *testing.T) {
		verifyZFSSetupTF(t, ctx, instanceName, zone)
	})

	// Step 5: Create container with test data
	testData := ""
	expectedChecksum := ""
	t.Run("CreateContainerWithData", func(t *testing.T) {
		testData, expectedChecksum = createContainerWithTestDataTF(t, ctx, instanceName, zone)
		require.NotEmpty(t, testData, "Test data should not be empty")
		require.NotEmpty(t, expectedChecksum, "Checksum should not be empty")
		t.Logf("Test data written with checksum: %s", expectedChecksum)
	})

	// Step 6: Reboot instance using gcloud (Terraform doesn't support reboot action)
	t.Run("RebootInstance", func(t *testing.T) {
		rebootInstanceTF(t, ctx, instanceName, zone)
	})

	// Step 7: Wait for instance to come back
	t.Run("WaitAfterReboot", func(t *testing.T) {
		waitForInstanceReadyTF(t, ctx, instanceName, zone)
		t.Log("Instance is back online after reboot")
	})

	// Step 8: Verify data persisted
	t.Run("VerifyDataPersistence", func(t *testing.T) {
		verifyDataPersistedTF(t, ctx, instanceName, zone, testData, expectedChecksum)
	})

	t.Log("✅ E2E Reboot Persistence Test with Terraform PASSED!")
}

// setupTerraformWorkspace creates a temporary Terraform workspace for testing.
// Copies the full terraform tree (consumer + module) so relative module paths work.
func setupTerraformWorkspace(t *testing.T) string {
	// Create temp directory for test workspace
	tmpDir, err := os.MkdirTemp("", "containarium-e2e-*")
	require.NoError(t, err, "Failed to create temp directory")
