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
