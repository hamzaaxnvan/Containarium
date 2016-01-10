package incus

import (
	"strings"
	"testing"
)

func TestClassifyGPUInput(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantPCI   string
		wantNeeds bool // true if classifier says "needs resolution"
		wantErr   string
	}{
		{name: "empty", input: "", wantErr: "empty"},
		{name: "PCI shaped", input: "0000:0b:00.0", wantPCI: "0000:0b:00.0"},
		{name: "PCI shaped uppercase", input: "0000:0B:00.0", wantPCI: "0000:0B:00.0"},
		{name: "PCI without domain", input: "0b:00.0", wantPCI: "0b:00.0"},
		{name: "numeric zero", input: "0", wantNeeds: true},
		{name: "numeric one", input: "1", wantNeeds: true},
		{name: "negative", input: "-1", wantErr: "non-negative integer"},
		{name: "non-numeric letters", input: "abc", wantErr: "non-negative integer"},
		{name: "decimal", input: "1.5", wantErr: "non-negative integer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pci, ok, err := classifyGPUInput(tc.input)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got err=%v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tc.wantPCI != "" && pci != tc.wantPCI {
				t.Errorf("pci: got %q, want %q", pci, tc.wantPCI)
			}
			if !tc.wantNeeds && pci == "" {
				t.Errorf("expected PCI passthrough, got empty (ok=%v)", ok)
			}
			if tc.wantNeeds && ok {
				t.Errorf("expected needs-resolution, got ok=true")
			}
		})
	}
}
