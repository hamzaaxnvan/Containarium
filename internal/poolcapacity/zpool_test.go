package poolcapacity

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// fakeRun scripts the command runner by zpool name and records every call.
type fakeRun struct {
	mu    sync.Mutex
	calls [][]string
	out   map[string]string // pool -> stdout
	err   map[string]error  // pool -> error
	hang  map[string]bool   // pool -> block until the deadline
}

func (f *fakeRun) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	pool := args[len(args)-1]
	if f.hang[pool] {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := f.err[pool]; err != nil {
		return nil, err
	}
	return []byte(f.out[pool]), nil
}

func (f *fakeRun) called() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func baseRead(pools ...incus.StoragePoolUsage) Reader {
	return func() ([]incus.StoragePoolUsage, error) { return append([]incus.StoragePoolUsage(nil), pools...), nil }
}

func zfsPool(name, zpool string) incus.StoragePoolUsage {
	return incus.StoragePoolUsage{Name: name, Driver: "zfs", ZpoolName: zpool, UsedBytes: 50, TotalBytes: 100}
}

// #2853: the command, its arguments and the parsed numbers.
func TestWithZpool_ReadsCapacityAndFragmentation(t *testing.T) {
	f := &fakeRun{out: map[string]string{"tank": "tank\t37\t12\n"}}
	got, err := WithZpool(baseRead(zfsPool("default", "tank")), f.run, time.Second)()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || strings.Join(f.calls[0], " ") != "zpool list -Hp -o name,cap,frag tank" {
		t.Fatalf("calls = %v, want exactly `zpool list -Hp -o name,cap,frag tank`", f.calls)
	}
	z := got[0].Zpool
	if z == nil || z.CapacityPercent != 37 || !z.FragmentationKnown || z.FragmentationPercent != 12 {
		t.Fatalf("Zpool = %+v, want cap 37 frag 12", z)
	}
	if got[0].UsedBytes != 50 || got[0].TotalBytes != 100 {
		t.Errorf("the Incus numbers were disturbed: %+v", got[0])
	}
}

func TestWithZpool_ParsesWhatZpoolActuallyPrints(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		wantOK    bool
		cap, frag int64
		fragKnown bool
	}{
		{"normal", "tank\t37\t12\n", true, 37, 12, true},
		{"spaces instead of tabs", "tank 37 12\n", true, 37, 12, true},
		{"fragmentation not available", "tank\t37\t-\n", true, 37, 0, false},
		{"percent signs kept", "tank\t37%\t12%\n", true, 37, 12, true},
		{"percent sign with no fragmentation", "tank\t37%\t-\n", true, 37, 0, false},
		{"full pool", "tank\t100\t88\n", true, 100, 88, true},
		{"empty", "", false, 0, 0, false},
		{"too few fields", "tank\t37\n", false, 0, 0, false},
		{"not a number", "tank\tabc\t12\n", false, 0, 0, false},
		{"out of range", "tank\t250\t12\n", false, 0, 0, false},
		{"negative", "tank\t-5\t12\n", false, 0, 0, false},
		{"another pool's line", "other\t37\t12\n", false, 0, 0, false},
		{"trailing noise lines", "tank\t37\t12\nwarning: something\n", true, 37, 12, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRun{out: map[string]string{"tank": tc.out}}
			got, _ := WithZpool(baseRead(zfsPool("default", "tank")), f.run, time.Second)()
			z := got[0].Zpool
			if !tc.wantOK {
				if z != nil {
					t.Fatalf("Zpool = %+v from %q, want none", z, tc.out)
				}
				return
			}
			if z == nil || z.CapacityPercent != tc.cap || z.FragmentationKnown != tc.fragKnown || z.FragmentationPercent != tc.frag {
				t.Fatalf("Zpool = %+v from %q, want cap %d frag %d known=%v", z, tc.out, tc.cap, tc.frag, tc.fragKnown)
			}
		})
	}
}

// Only a ZFS pool with a known zpool name is asked.
func TestWithZpool_SkipsPoolsThatAreNotZFS(t *testing.T) {
	f := &fakeRun{out: map[string]string{"tank": "tank\t37\t12\n"}}
	read := baseRead(
		incus.StoragePoolUsage{Name: "dirpool", Driver: "dir", TotalBytes: 100},
		incus.StoragePoolUsage{Name: "zfs-no-name", Driver: "zfs", TotalBytes: 100}, // name not derivable
		zfsPool("default", "tank"),
	)
	got, err := WithZpool(read, f.run, time.Second)()
	if err != nil {
		t.Fatal(err)
	}
	if f.called() != 1 {
		t.Fatalf("zpool was run %d times, want 1 (only the ZFS pool with a name)", f.called())
	}
	if got[0].Zpool != nil || got[1].Zpool != nil || got[2].Zpool == nil {
		t.Fatalf("Zpool by pool = %v %v %v, want only the third", got[0].Zpool, got[1].Zpool, got[2].Zpool)
	}
