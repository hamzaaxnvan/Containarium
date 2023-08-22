//go:build !containarium_client

package server

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The sweep's box scripts run here with the local sh against a temp directory
// standing in for a box user's home, the way `incus exec <box> sh -c` runs them
// in the box (#3296).

var sweepNow = time.Now().Truncate(time.Second)

// checkoutHome makes a temp home with an empty ~/work and returns the home's
// real path (pwd -P must equal <home>/work).
func checkoutHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// setAge sets the modification time of path and everything under it to at,
// symlinks included (touch -h: the link itself, never its target).
func setAge(t *testing.T, path string, at time.Time) {
	t.Helper()
	var paths []string
	if err := filepath.Walk(path, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Deepest first, so touching a child does not bump its parent afterwards.
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, p := range paths {
		if out, err := osexec.Command("touch", "-h", "-d", "@"+fmt.Sprint(at.Unix()), p).CombinedOutput(); err != nil {
			t.Fatalf("touch %s: %v: %s", p, err, out)
		}
	}
}

// checkoutDir makes work/<name> holding one file and ages it all to at.
func checkoutDir(t *testing.T, home, name string, at time.Time) string {
	t.Helper()
	d := filepath.Join(home, "work", name)
	if err := os.MkdirAll(filepath.Join(d, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(d, "src", "main.go"), "package main\n")
	setAge(t, d, at)
	return d
}

func localExec(_ string, script string) (string, error) {
	out, err := osexec.Command("sh", "-c", script).Output()
	if err != nil {
		if ee, ok := err.(*osexec.ExitError); ok {
			return "", fmt.Errorf("%v: %s", err, ee.Stderr)
		}
		return "", err
	}
	return string(out), nil
}

// logRecorder collects the sweep's log lines.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (l *logRecorder) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logRecorder) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

const (
	dispRemoved     = pb.BoxRunCheckoutDisposition_BOX_RUN_CHECKOUT_DISPOSITION_REMOVED
	dispWouldRemove = pb.BoxRunCheckoutDisposition_BOX_RUN_CHECKOUT_DISPOSITION_WOULD_REMOVE
	dispRecent      = pb.BoxRunCheckoutDisposition_BOX_RUN_CHECKOUT_DISPOSITION_KEPT_RECENT
	dispLive        = pb.BoxRunCheckoutDisposition_BOX_RUN_CHECKOUT_DISPOSITION_KEPT_LIVE_RUN
	dispInUse       = pb.BoxRunCheckoutDisposition_BOX_RUN_CHECKOUT_DISPOSITION_KEPT_IN_USE
	dispSymlink     = pb.BoxRunCheckoutDisposition_BOX_RUN_CHECKOUT_DISPOSITION_KEPT_SYMLINK
	dispNotDir      = pb.BoxRunCheckoutDisposition_BOX_RUN_CHECKOUT_DISPOSITION_KEPT_NOT_DIRECTORY
	dispChanged     = pb.BoxRunCheckoutDisposition_BOX_RUN_CHECKOUT_DISPOSITION_KEPT_CHANGED
)

func TestSweepHome_Table(t *testing.T) {
	const name = "repo-run-0123abcd"
	old := sweepNow.Add(-10 * 24 * time.Hour)
	tests := []struct {
		name   string
		maxAge time.Duration
		dryRun bool
		// setup builds the home and returns live-run facts and the paths that
		// must still exist afterwards (gone lists those that must not).
		setup func(t *testing.T, home string) (live runCheckoutLiveRuns, kept, gone []string)
		// want is the reported disposition per entry name; an absent name must
		// not be reported at all.
		want map[string]pb.BoxRunCheckoutDisposition
	}{
		{
			name: "old unused checkout is removed", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				return runCheckoutLiveRuns{}, nil, []string{checkoutDir(t, home, name, old)}
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispRemoved},
		},
		{
			name: "dry run lists it and removes nothing", maxAge: 7 * 24 * time.Hour, dryRun: true,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				return runCheckoutLiveRuns{}, []string{checkoutDir(t, home, name, old)}, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispWouldRemove},
		},
		{
			name: "newer than the threshold is kept", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				return runCheckoutLiveRuns{}, []string{checkoutDir(t, home, name, sweepNow.Add(-24*time.Hour))}, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispRecent},
		},
		{
			name: "an old directory with one recent file inside is kept", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				d := checkoutDir(t, home, name, old)
				setAge(t, filepath.Join(d, "src", "main.go"), sweepNow.Add(-time.Hour))
				return runCheckoutLiveRuns{}, []string{d}, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispRecent},
		},
		{
			name: "the threshold is settable: 1h removes a 2h-old checkout", maxAge: time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				return runCheckoutLiveRuns{}, nil, []string{checkoutDir(t, home, name, sweepNow.Add(-2*time.Hour))}
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispRemoved},
		},
		{
			name: "the threshold is settable: 30d keeps a 10d-old checkout", maxAge: 30 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				return runCheckoutLiveRuns{}, []string{checkoutDir(t, home, name, old)}, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispRecent},
		},
		{
			name: "modified after a live run started: kept", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				d := checkoutDir(t, home, name, old)
				return runCheckoutLiveRuns{since: old.Add(-time.Hour)}, []string{d}, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispLive},
		},
		{
			name: "a live run's recorded cwd is inside it: kept", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				d := checkoutDir(t, home, name, old)
				return runCheckoutLiveRuns{since: sweepNow, cwds: []string{d + "/src"}}, []string{d}, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispLive},
		},
		{
			name: "a live run that started after the checkout's last change does not protect it", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				return runCheckoutLiveRuns{since: sweepNow.Add(-time.Hour)}, nil, []string{checkoutDir(t, home, name, old)}
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispRemoved},
		},
		{
			name: "a process with its cwd inside it: kept", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				d := checkoutDir(t, home, name, old)
				cmd := osexec.Command("sleep", "60")
				cmd.Dir = filepath.Join(d, "src")
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
				return runCheckoutLiveRuns{}, []string{d}, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispInUse},
		},
		{
			name: "non-matching names are never touched or listed", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				var kept []string
				for _, n := range []string{"repo", "repo-run-0123ABCD", "repo-run-0123abc", "repo-run-0123abcde", "-run-0123abcd", "repo-run-0123abcd.bak", "repo run-0123abcd"} {
					kept = append(kept, checkoutDir(t, home, n, old))
				}
				return runCheckoutLiveRuns{}, kept, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{},
		},
		{
			name: "a matching symlink pointing outside work is kept, and so is its target", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				outside := filepath.Join(home, "outside")
				if err := os.MkdirAll(outside, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(outside, "precious"), "x")
				setAge(t, outside, old)
				link := filepath.Join(home, "work", name)
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
				setAge(t, link, old)
				return runCheckoutLiveRuns{}, []string{link, filepath.Join(outside, "precious")}, nil
			},
