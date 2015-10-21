package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

type fakeReports struct {
	saved   []*Report
	saveErr error
}

func (f *fakeReports) SaveReport(_ context.Context, r *Report) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	cp := *r
	f.saved = append(f.saved, &cp)
	return nil
}

type hookCall struct{ container, username, status string }

// fakeTenantBox stands in for the tenant's container. Each scripted exec is keyed by
// a substring of the joined argv.
type fakeTenantBox struct {
	info    *incus.ContainerInfo
	infoErr error

	hasClamscan   bool
	hasSignatures bool
	version       string
	scanOut       string
	scanCode      int
	scanErr       error
	scanArgv      []string
	execs         [][]string
}

func (f *fakeTenantBox) GetContainer(string) (*incus.ContainerInfo, error) { return f.info, f.infoErr }

func (f *fakeTenantBox) ExecWithExitCode(_ string, cmd []string) (string, string, int, error) {
	f.execs = append(f.execs, cmd)
	j := strings.Join(cmd, " ")
	switch {
	case strings.Contains(j, "command -v clamscan"):
		if f.hasClamscan {
			return "/usr/bin/clamscan\n", "", 0, nil
		}
		return "", "", 1, nil
	case strings.Contains(j, "/var/lib/clamav"):
		if f.hasSignatures {
			return "", "", 0, nil
		}
		return "", "", 2, nil
	case strings.HasSuffix(j, "--version") && cmd[0] == "clamscan":
		return f.version + "\nsecond line\n", "", 0, nil
	case cmd[0] == "timeout":
		f.scanArgv = cmd
		return f.scanOut, "", f.scanCode, f.scanErr
	}
	return "", "", 0, errors.New("unexpected command " + j)
}

func readyTenantBox() *fakeTenantBox {
	return &fakeTenantBox{
		info:          &incus.ContainerInfo{Name: "alice-container", State: "Running", Memory: "4GB"},
		hasClamscan:   true,
		hasSignatures: true,
		version:       "ClamAV 1.0.7/27123/Sun Oct 18",
	}
}

func newTestScanner(box *fakeTenantBox, rep *fakeReports, hook *[]hookCall) *Scanner {
	s := &Scanner{box: box, reports: rep, timeout: time.Minute}
	s.SetScanResultHook(func(c, u, st string) { *hook = append(*hook, hookCall{c, u, st}) })
	return s
}

func runScan(t *testing.T, box *fakeTenantBox) (*Report, []hookCall) {
	t.Helper()
	var hook []hookCall
	rep := &fakeReports{}
	if err := newTestScanner(box, rep, &hook).ScanContainer(context.Background(), "alice-container", "alice"); err != nil {
		t.Fatalf("ScanContainer: %v", err)
	}
	if len(rep.saved) != 1 {
		t.Fatalf("saved %d reports, want 1", len(rep.saved))
	}
	return rep.saved[0], hook
}

func TestScanContainer_Outcomes(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*fakeTenantBox)
		wantStatus string
		wantInText string // substring expected in Findings (the reason / version / signatures)
		wantCount  int
		wantScan   bool // whether clamscan itself must have been run
	}{
		{"clean run records only the scanner version", func(b *fakeTenantBox) {}, ScanStatusClean, "ClamAV 1.0.7/27123/Sun Oct 18", 0, true},
		{"infected keeps signature names and counts, not paths", func(b *fakeTenantBox) {
			b.scanCode = 1
			b.scanOut = "/home/alice/secret plans/a.exe: Win.Test.EICAR_HDB-1 FOUND\n/home/alice/b.com: Win.Test.EICAR_HDB-1 FOUND\n/tmp/x: Eicar-Signature FOUND\n"
		}, ScanStatusInfected, "Win.Test.EICAR_HDB-1 (2)", 3, true},
		{"scan error is incomplete", func(b *fakeTenantBox) { b.scanCode = 2 }, ScanStatusIncomplete, "scan error", 0, true},
		{"killed (OOM) is incomplete", func(b *fakeTenantBox) { b.scanCode = 137 }, ScanStatusIncomplete, "out of memory", 0, true},
		{"timeout is incomplete", func(b *fakeTenantBox) { b.scanCode = 124 }, ScanStatusIncomplete, "timed out", 0, true},
		{"unknown exit code is incomplete", func(b *fakeTenantBox) { b.scanCode = 77 }, ScanStatusIncomplete, "77", 0, true},
		{"exec failure is incomplete", func(b *fakeTenantBox) { b.scanErr = errors.New("socket wedged") }, ScanStatusIncomplete, "socket wedged", 0, true},
		{"stopped box is incomplete and not scanned", func(b *fakeTenantBox) { b.info.State = "Stopped" }, ScanStatusIncomplete, "not running", 0, false},
		{"under 4 GB is unsupported_size and not scanned", func(b *fakeTenantBox) { b.info.Memory = "2GB" }, ScanStatusUnsupportedSize, "below the 4 GB", 0, false},
		{"clamscan missing is not_installed", func(b *fakeTenantBox) { b.hasClamscan = false }, ScanStatusNotInstalled, "not installed", 0, false},
		{"signatures missing is not_installed", func(b *fakeTenantBox) { b.hasSignatures = false }, ScanStatusNotInstalled, "signatures", 0, false},
		{"lookup failure is incomplete", func(b *fakeTenantBox) { b.infoErr = errors.New("no such box") }, ScanStatusIncomplete, "no such box", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			box := readyTenantBox()
			tc.mutate(box)
			r, hook := runScan(t, box)
			if r.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q (findings %q)", r.Status, tc.wantStatus, r.Findings)
			}
			if !strings.Contains(r.Findings, tc.wantInText) {
				t.Errorf("findings %q should contain %q", r.Findings, tc.wantInText)
			}
			if r.FindingsCount != tc.wantCount {
				t.Errorf("findings count = %d, want %d", r.FindingsCount, tc.wantCount)
			}
