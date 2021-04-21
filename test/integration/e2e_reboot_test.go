package integration

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2ERebootPersistence is an end-to-end test that:
// 1. Creates a GCE instance with ZFS
// 2. Sets up firewall rules
// 3. Creates a guest container
// 4. Writes test data
// 5. Reboots the instance
// 6. Verifies data persists
func TestE2ERebootPersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Check required environment variables
	projectID := os.Getenv("GCP_PROJECT")
	if projectID == "" {
		t.Skip("GCP_PROJECT not set, skipping E2E test")
	}

	zone := getEnv("GCP_ZONE", "asia-east1-a")
	instanceName := fmt.Sprintf("containarium-e2e-test-%d", time.Now().Unix())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	t.Logf("Starting E2E reboot persistence test...")
	t.Logf("  Project: %s", projectID)
	t.Logf("  Zone: %s", zone)
	t.Logf("  Instance: %s", instanceName)

	// Cleanup function
	defer func() {
		if !t.Failed() || os.Getenv("KEEP_INSTANCE") == "true" {
			// Only cleanup if test passed or KEEP_INSTANCE not set
			t.Log("Cleaning up test instance...")
			cleanupInstance(t, projectID, zone, instanceName)
		} else {
			t.Logf("Test failed - keeping instance %s for debugging", instanceName)
			t.Logf("To cleanup: gcloud compute instances delete %s --zone=%s --quiet", instanceName, zone)
		}
	}()

	// Step 1: Create instance
	t.Run("CreateInstance", func(t *testing.T) {
		createTestInstance(t, ctx, projectID, zone, instanceName)
	})

	// Step 2: Wait for instance to be ready
	instanceIP := ""
	t.Run("WaitForInstance", func(t *testing.T) {
		instanceIP = waitForInstanceReady(t, ctx, projectID, zone, instanceName)
		require.NotEmpty(t, instanceIP, "Instance IP should not be empty")
		t.Logf("Instance ready at IP: %s", instanceIP)
	})

	// Step 3: Verify ZFS setup
	t.Run("VerifyZFS", func(t *testing.T) {
		verifyZFSSetup(t, ctx, projectID, zone, instanceName)
	})

	// Step 4: Create container with test data
	testData := ""
	expectedChecksum := ""
	t.Run("CreateContainerWithData", func(t *testing.T) {
		testData, expectedChecksum = createContainerWithTestData(t, ctx, projectID, zone, instanceName)
		require.NotEmpty(t, testData, "Test data should not be empty")
		require.NotEmpty(t, expectedChecksum, "Checksum should not be empty")
		t.Logf("Test data written with checksum: %s", expectedChecksum)
	})

	// Step 5: Reboot instance
	t.Run("RebootInstance", func(t *testing.T) {
		rebootInstance(t, ctx, projectID, zone, instanceName)
	})

	// Step 6: Wait for instance to come back
	t.Run("WaitAfterReboot", func(t *testing.T) {
		waitForInstanceReady(t, ctx, projectID, zone, instanceName)
		t.Log("Instance is back online after reboot")
	})

	// Step 7: Verify data persisted
	t.Run("VerifyDataPersistence", func(t *testing.T) {
		verifyDataPersisted(t, ctx, projectID, zone, instanceName, testData, expectedChecksum)
	})

	t.Log("✅ E2E Reboot Persistence Test PASSED!")
}

// createTestInstance creates a GCE instance with ZFS configuration
func createTestInstance(t *testing.T, ctx context.Context, projectID, zone, instanceName string) {
	t.Log("Creating GCE instance with ZFS...")

	// Get the current directory to find startup script
	startupScript := "../../terraform/gce/scripts/startup-spot.sh"
	if _, err := os.Stat(startupScript); os.IsNotExist(err) {
		t.Fatalf("Startup script not found: %s", startupScript)
	}

	// Create instance using gcloud
	cmd := exec.CommandContext(ctx, "gcloud", "compute", "instances", "create", instanceName,
		"--project="+projectID,
		"--zone="+zone,
		"--machine-type=e2-standard-2",
		"--image-family=ubuntu-2404-lts-amd64",
		"--image-project=ubuntu-os-cloud",
		"--boot-disk-size=100GB",
		"--boot-disk-type=pd-balanced",
		"--metadata-from-file=startup-script="+startupScript,
		"--metadata=incus_version=,admin_users=testuser,enable_monitoring=false,USE_PERSISTENT_DISK=true,containarium_version=dev,containarium_binary_url=",
		"--tags=containarium-server",
		"--provisioning-model=SPOT",
		"--instance-termination-action=STOP",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to create instance: %v\nOutput: %s", err, output)
	}

	// Create persistent disk
	diskName := instanceName + "-data"
	cmd = exec.CommandContext(ctx, "gcloud", "compute", "disks", "create", diskName,
		"--project="+projectID,
		"--zone="+zone,
		"--size=100GB",
		"--type=pd-balanced",
	)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Logf("Warning: Failed to create persistent disk: %v\nOutput: %s", err, output)
	}

	// Attach disk
	cmd = exec.CommandContext(ctx, "gcloud", "compute", "instances", "attach-disk", instanceName,
		"--project="+projectID,
		"--zone="+zone,
		"--disk="+diskName,
		"--device-name=incus-data",
	)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Logf("Warning: Failed to attach disk: %v\nOutput: %s", err, output)
	}

	// Create firewall rules
	createFirewallRules(t, ctx, projectID)

	t.Log("✓ Instance created successfully")
}

// createFirewallRules creates necessary firewall rules
func createFirewallRules(t *testing.T, ctx context.Context, projectID string) {
	t.Log("Creating firewall rules...")

	// SSH rule
	cmd := exec.CommandContext(ctx, "gcloud", "compute", "firewall-rules", "create",
		"containarium-e2e-ssh",
		"--project="+projectID,
		"--allow=tcp:22",
		"--target-tags=containarium-server",
		"--source-ranges=0.0.0.0/0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "already exists") {
		t.Logf("Warning: Failed to create SSH firewall rule: %v\nOutput: %s", err, output)
	}

	// gRPC rule
	cmd = exec.CommandContext(ctx, "gcloud", "compute", "firewall-rules", "create",
		"containarium-e2e-grpc",
		"--project="+projectID,
		"--allow=tcp:50051",
		"--target-tags=containarium-server",
		"--source-ranges=0.0.0.0/0",
	)
	output, err = cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "already exists") {
		t.Logf("Warning: Failed to create gRPC firewall rule: %v\nOutput: %s", err, output)
	}

	t.Log("✓ Firewall rules created")
}

// waitForInstanceReady waits for the instance to be fully ready
func waitForInstanceReady(t *testing.T, ctx context.Context, projectID, zone, instanceName string) string {
	t.Log("Waiting for instance to be ready...")

	// Wait for instance to be RUNNING
	maxAttempts := 60
	for i := 0; i < maxAttempts; i++ {
		cmd := exec.CommandContext(ctx, "gcloud", "compute", "instances", "describe", instanceName,
			"--project="+projectID,
			"--zone="+zone,
			"--format=get(status)",
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("Attempt %d/%d: Instance not ready yet", i+1, maxAttempts)
			time.Sleep(10 * time.Second)
			continue
		}

		status := strings.TrimSpace(string(output))
		if status == "RUNNING" {
			break
		}

		t.Logf("Attempt %d/%d: Instance status: %s", i+1, maxAttempts, status)
		time.Sleep(10 * time.Second)
	}

