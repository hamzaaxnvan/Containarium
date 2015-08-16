package poolcapacity

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Pool headroom floor (#3128): below a hard floor, creates and snapshots are
// refused instead of filling the pool to 0 B (#3043).

func TestFloors_Set(t *testing.T) {
	tests := []struct {
		spec    string
		pool    string
		pct     int
		wantErr string
	}{
		{spec: "default=90", pool: "default", pct: 90},
		{spec: "fast=86", pool: "fast", pct: 86},
		{spec: "fast=99", pool: "fast", pct: 99},
		{spec: " tank = 97 ", pool: "tank", pct: 97},
		{spec: "fast", wantErr: "<pool>=<percent>"},
		{spec: "=90", wantErr: "pool name"},
		{spec: "fast=", wantErr: "percent"},
		{spec: "fast=abc", wantErr: "percent"},
		{spec: "fast=90.5", wantErr: "percent"},
		{spec: "fast=49", wantErr: "between 50 and 99"},
		{spec: "fast=100", wantErr: "between 50 and 99"},
		{spec: "fast=50", wantErr: "above the 85%"},
		{spec: "fast=85", wantErr: "above the 85%"},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			f := Floors{}
			err := f.Set(tt.spec)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Set(%q) error = %v, want one containing %q", tt.spec, err, tt.wantErr)
				}
				if len(f) != 0 {
					t.Errorf("a rejected spec was stored: %v", f)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tt.spec, err)
			}
			if got := f.For(tt.pool); got != tt.pct {
				t.Errorf("For(%q) = %d, want %d", tt.pool, got, tt.pct)
			}
		})
	}
}

// The flag is repeatable; naming one pool twice is a typo, not an override.
func TestFloors_SetRejectsADuplicatePool(t *testing.T) {
	f := Floors{}
	if err := f.Set("fast=90"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("slow=92"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("fast=97"); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate pool: err = %v, want a 'more than once' error", err)
	}
	if f.For("fast") != 90 || f.For("slow") != 92 {
		t.Errorf("floors = %v", f)
	}
}

func TestFloors_DefaultForUnlistedPool(t *testing.T) {
	var f Floors // nil map: no flag given at all
	if got := f.For("anything"); got != DefaultFloorPercent || DefaultFloorPercent != 95 {
		t.Errorf("For(unlisted) = %d (DefaultFloorPercent %d), want 95", got, DefaultFloorPercent)
	}
	if f.String() != "" {
		t.Errorf("empty Floors String() = %q, want empty", f.String())
	}
	f = Floors{"b": 90, "a": 97}
	if got := f.String(); got != "a=97,b=90" {
		t.Errorf("String() = %q, want sorted a=97,b=90", got)
	}
	if f.Type() == "" {
		t.Error("Type() must name the value for --help")
	}
}

func snapOf(pools ...incus.StoragePoolUsage) func() Snapshot {
	return func() Snapshot {
		s := Snapshot{}
		for _, p := range pools {
			s.Pools = append(s.Pools, Sample{StoragePoolUsage: p, Level: Classify(p.UsedBytes, p.TotalBytes)})
		}
		return s
	}
}

func gibPool(name string, usedGiB, totalGiB int64) incus.StoragePoolUsage {
	return incus.StoragePoolUsage{Name: name, Driver: "zfs", UsedBytes: usedGiB << 30, TotalBytes: totalGiB << 30}
}

func TestGuard_Admit(t *testing.T) {
	tests := []struct {
		name    string
		floors  Floors
		pools   []incus.StoragePoolUsage
		pool    string
		op      Operation
		refused bool
		warned  bool
	}{
		{name: "well below the floor", pools: []incus.StoragePoolUsage{gibPool("default", 50, 100)}, pool: "default", op: OperationCreate},
		{name: "past the high-water mark but above the floor", pools: []incus.StoragePoolUsage{gibPool("default", 94, 100)}, pool: "default", op: OperationSnapshot},
		{name: "exactly at the default floor", pools: []incus.StoragePoolUsage{gibPool("default", 95, 100)}, pool: "default", op: OperationCreate, refused: true},
		{name: "past the default floor, snapshot", pools: []incus.StoragePoolUsage{gibPool("default", 99, 100)}, pool: "default", op: OperationSnapshot, refused: true},
		{name: "a full pool elsewhere does not block this one", pools: []incus.StoragePoolUsage{gibPool("default", 10, 100), gibPool("other", 100, 100)}, pool: "default", op: OperationCreate},
		{name: "per-pool floor lower than default", floors: Floors{"fast": 90}, pools: []incus.StoragePoolUsage{gibPool("fast", 92, 100)}, pool: "fast", op: OperationCreate, refused: true},
