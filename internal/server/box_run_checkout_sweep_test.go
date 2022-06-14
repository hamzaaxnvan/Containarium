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
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispSymlink},
		},
		{
			name: "a symlink inside an old checkout is removed as a link, its outside target stays", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				outside := filepath.Join(home, "outside")
				if err := os.MkdirAll(outside, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(outside, "precious"), "x")
				d := checkoutDir(t, home, name, old)
				if err := os.Symlink(outside, filepath.Join(d, "escape")); err != nil {
					t.Fatal(err)
				}
				setAge(t, d, old)
				return runCheckoutLiveRuns{}, []string{filepath.Join(outside, "precious")}, []string{d}
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispRemoved},
		},
		{
			name: "a matching regular file is kept", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				f := filepath.Join(home, "work", name)
				writeFile(t, f, "x")
				setAge(t, f, old)
				return runCheckoutLiveRuns{}, []string{f}, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{name: dispNotDir},
		},
		{
			name: "work as a symlink out of the home is not swept at all", maxAge: 7 * 24 * time.Hour,
			setup: func(t *testing.T, home string) (runCheckoutLiveRuns, []string, []string) {
				elsewhere := filepath.Join(home, "elsewhere")
				if err := os.Mkdir(elsewhere, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(home, "work")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(elsewhere, filepath.Join(home, "work")); err != nil {
					t.Fatal(err)
				}
				d := filepath.Join(elsewhere, name)
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
				setAge(t, d, old)
				return runCheckoutLiveRuns{}, []string{d}, nil
			},
			want: map[string]pb.BoxRunCheckoutDisposition{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := checkoutHome(t)
			live, kept, gone := tt.setup(t, home)
			logs := &logRecorder{}
			sw := runCheckoutSweep{exec: localExec, box: "alice-container", now: sweepNow, maxAge: tt.maxAge, dryRun: tt.dryRun, live: live, logf: logs.logf}
			got, err := sw.sweepHome("alice", home)
			if err != nil {
				t.Fatalf("sweepHome: %v", err)
			}
			gotMap := map[string]pb.BoxRunCheckoutDisposition{}
			for _, c := range got {
				gotMap[c.GetName()] = c.GetDisposition()
				if c.GetUser() != "alice" {
					t.Errorf("%s: user = %q", c.GetName(), c.GetUser())
				}
			}
			if fmt.Sprint(gotMap) != fmt.Sprint(tt.want) {
				t.Errorf("dispositions = %v, want %v", gotMap, tt.want)
			}
			for _, p := range kept {
				if !exists(p) {
					t.Errorf("%s was removed; it must be kept", p)
				}
			}
			for _, p := range gone {
				if exists(p) {
					t.Errorf("%s still exists; it must be removed", p)
				}
			}
			removed := 0
			for _, c := range got {
				if c.GetDisposition() == dispRemoved {
					removed++
					if !strings.Contains(logs.all(), "removed "+c.GetName()+" ") || !strings.Contains(logs.all(), fmt.Sprintf("(%d bytes)", c.GetSizeBytes())) || c.GetSizeBytes() <= 0 {
						t.Errorf("removal of %s (size %d) not logged with name and size: %q", c.GetName(), c.GetSizeBytes(), logs.all())
					}
				}
			}
			if removed == 0 && strings.Contains(logs.all(), "removed ") {
				t.Errorf("logged a removal that did not happen: %q", logs.all())
			}
			if strings.Contains(logs.all(), home) {
				t.Errorf("log names a path outside ~/work: %q", logs.all())
			}
		})
	}
}

// The dry run's list is exactly what a real sweep removes, and the dry run
// removes nothing.
func TestSweepHome_DryRunListsExactlyTheRealSet(t *testing.T) {
	home := checkoutHome(t)
	old := sweepNow.Add(-10 * 24 * time.Hour)
	for _, n := range []string{"a-run-00000001", "b-run-00000002", ".hidden-run-0000000f"} {
		checkoutDir(t, home, n, old)
	}
	checkoutDir(t, home, "c-run-00000003", sweepNow)
	checkoutDir(t, home, "mine", old)
	link := filepath.Join(home, "work", "d-run-00000004")
	if err := os.Symlink(filepath.Join(home, "work", "mine"), link); err != nil {
		t.Fatal(err)
	}

	set := func(cs []*pb.BoxRunCheckout, d pb.BoxRunCheckoutDisposition) []string {
		var out []string
		for _, c := range cs {
			if c.GetDisposition() == d {
				out = append(out, c.GetName())
			}
		}
		sort.Strings(out)
		return out
	}
	sw := runCheckoutSweep{exec: localExec, box: "b", now: sweepNow, maxAge: 7 * 24 * time.Hour, dryRun: true, logf: func(string, ...any) {}}
	dry, err := sw.sweepHome("alice", home)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(home, "work"))
	if len(entries) != 6 {
		t.Fatalf("the dry run changed ~/work: %d entries left", len(entries))
	}
	sw.dryRun = false
	real, err := sw.sweepHome("alice", home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".hidden-run-0000000f", "a-run-00000001", "b-run-00000002"}
	if d, r := set(dry, dispWouldRemove), set(real, dispRemoved); fmt.Sprint(d) != fmt.Sprint(want) || fmt.Sprint(r) != fmt.Sprint(want) {
		t.Fatalf("dry run %v, real run %v, want both %v", d, r, want)
	}
	for _, n := range want {
		if exists(filepath.Join(home, "work", n)) {
			t.Errorf("%s not removed", n)
		}
	}
	for _, n := range []string{"c-run-00000003", "mine", "d-run-00000004"} {
		if !exists(filepath.Join(home, "work", n)) {
			t.Errorf("%s removed", n)
		}
	}
}

// The removal script re-checks every guard in the box right before rm, so an
// entry that changed after it was listed is left alone.
func TestRunCheckoutRemoveScript_RechecksBeforeRemoving(t *testing.T) {
	old := sweepNow.Add(-10 * 24 * time.Hour)
	cutoff := sweepNow.Add(-7 * 24 * time.Hour).Unix()
	tests := []struct {
		name  string
		setup func(t *testing.T, home, entry string) (keep []string)
	}{
		{"swapped for a symlink to outside work", func(t *testing.T, home, entry string) []string {
			outside := filepath.Join(home, "outside")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(outside, "precious"), "x")
			setAge(t, outside, old)
			if err := os.Symlink(outside, entry); err != nil {
				t.Fatal(err)
			}
			// An old link, so only the symlink check (not the age check) refuses it.
			setAge(t, entry, old)
			return []string{entry, filepath.Join(outside, "precious")}
		}},
		{"modified since it was listed", func(t *testing.T, home, entry string) []string {
			checkoutDir(t, home, filepath.Base(entry), old)
			setAge(t, filepath.Join(entry, "src", "main.go"), sweepNow)
			return []string{entry}
		}},
		{"a process entered it", func(t *testing.T, home, entry string) []string {
			checkoutDir(t, home, filepath.Base(entry), old)
			cmd := osexec.Command("sleep", "60")
			cmd.Dir = entry
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
			return []string{entry}
		}},
		{"replaced by a regular file", func(t *testing.T, home, entry string) []string {
			writeFile(t, entry, "x")
			setAge(t, entry, old)
			return []string{entry}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := checkoutHome(t)
			entry := filepath.Join(home, "work", "repo-run-0123abcd")
			keep := tt.setup(t, home, entry)
			out, err := localExec("b", runCheckoutRemoveScript(home, []string{"repo-run-0123abcd"}, cutoff))
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(out) != "K repo-run-0123abcd" {
				t.Errorf("script output %q, want K", out)
			}
			for _, p := range keep {
				if !exists(p) {
					t.Errorf("%s removed", p)
				}
			}
		})
	}

	t.Run("a non-matching name is refused by the script itself", func(t *testing.T) {
		home := checkoutHome(t)
		d := checkoutDir(t, home, "repo", old)
		out, err := localExec("b", runCheckoutRemoveScript(home, []string{"repo"}, cutoff))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out) != "K repo" || !exists(d) {
			t.Errorf("output %q, exists %v", out, exists(d))
		}
	})
	t.Run("work swapped for a symlink before removal", func(t *testing.T) {
		home := checkoutHome(t)
		elsewhere := filepath.Join(home, "elsewhere")
		d := filepath.Join(elsewhere, "repo-run-0123abcd")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		setAge(t, d, old)
		if err := os.Remove(filepath.Join(home, "work")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(home, "work")); err != nil {
			t.Fatal(err)
		}
		out, err := localExec("b", runCheckoutRemoveScript(home, []string{"repo-run-0123abcd"}, cutoff))
