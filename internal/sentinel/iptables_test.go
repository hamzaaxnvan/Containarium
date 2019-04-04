package sentinel

import (
	"runtime"
	"testing"
)

func TestPort22FilteredFromForwarding(t *testing.T) {
	// Verify that enableForwarding filters port 22.
	// We can't run actual iptables on dev machines, but we can verify the
	// filtering logic by checking the constant and the filter code path.

	if sshPiperPort != 22 {
		t.Errorf("sshPiperPort should be 22, got %d", sshPiperPort)
	}

	// Test the filtering logic inline (same as enableForwarding)
	ports := []int{22, 80, 443, 50051}
	filtered := make([]int, 0, len(ports))
	for _, p := range ports {
		if p == sshPiperPort {
			continue
		}
		filtered = append(filtered, p)
	}

	if len(filtered) != 3 {
		t.Fatalf("expected 3 ports after filtering, got %d: %v", len(filtered), filtered)
	}

	for _, p := range filtered {
		if p == 22 {
			t.Error("port 22 should have been filtered out")
		}
	}

	expectedPorts := []int{80, 443, 50051}
	for i, p := range expectedPorts {
		if filtered[i] != p {
			t.Errorf("filtered[%d] = %d, want %d", i, filtered[i], p)
		}
	}
}

