package submit

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// fakeBox is a BoxRunner test double. execFunc/readFunc are nil-checked
// so a test only needs to set the one it cares about.
type fakeBox struct {
	execFunc func(container string, command []string) (stdout, stderr string, err error)
	readFunc func(container, path string) ([]byte, error)

	execCalls []([]string)
	readCalls []string
}

func (f *fakeBox) ExecWithOutput(container string, command []string) (string, string, error) {
	f.execCalls = append(f.execCalls, command)
	if f.execFunc != nil {
		return f.execFunc(container, command)
	}
	return "", "", nil
}

func (f *fakeBox) ReadFile(container, path string) ([]byte, error) {
	f.readCalls = append(f.readCalls, path)
	if f.readFunc != nil {
		return f.readFunc(container, path)
	}
	return nil, nil
}

// validBundleBytes returns real bundle bytes (from an actual git repo)
// so tests exercising the "malformed" check have a genuinely valid
// bundle to contrast against, and the "happy path" test doesn't rely on
// a hand-rolled fixture that might not match real git's format.
func validBundleBytes(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(dir+"/f.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "f.txt")
	testGit(t, dir, "commit", "-q", "-m", "base")
	base := testGit(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(dir+"/f.txt", []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "f.txt")
	testGit(t, dir, "commit", "-q", "-m", "head")
	bundlePath := dir + "/out.bundle"
	testGit(t, dir, "bundle", "create", bundlePath, base+"..HEAD")
	b, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBundle_Rejected(t *testing.T) {
	t.Run("box refuses an empty range", func(t *testing.T) {
		box := &fakeBox{execFunc: func(string, []string) (string, string, error) {
			return "", "fatal: Refusing to create empty bundle.", errors.New("exit status 128")
		}}
		_, err := ExtractBundle(box, "box1", "/workspace", "deadbeef", DefaultMaxBundleBytes)
		if err == nil {
			t.Fatal("want error, got nil")
		}
		if len(box.readCalls) != 0 {
			t.Errorf("ReadFile was called (%v); must not pull a bundle that failed to even build", box.readCalls)
		}
	})

	t.Run("over size cap is rejected without reading the file", func(t *testing.T) {
		box := &fakeBox{execFunc: func(_ string, cmd []string) (string, string, error) {
			script := strings.Join(cmd, " ")
			if strings.Contains(script, "wc -c") || strings.Contains(script, "stat") {
				return "999999999", "", nil
			}
			return "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n", "", nil
		}}
		_, err := ExtractBundle(box, "box1", "/workspace", "aaaa", 100)
		if !errors.Is(err, ErrBundleTooLarge) {
			t.Errorf("err = %v, want ErrBundleTooLarge", err)
		}
		if len(box.readCalls) != 0 {
			t.Errorf("ReadFile was called (%v); must reject on size before pulling the file", box.readCalls)
		}
	})

	t.Run("malformed content read back is rejected", func(t *testing.T) {
		box := &fakeBox{
			execFunc: func(_ string, cmd []string) (string, string, error) {
				script := strings.Join(cmd, " ")
				if strings.Contains(script, "wc -c") || strings.Contains(script, "stat") {
					return "7", "", nil
				}
				return "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n", "", nil
			},
			readFunc: func(string, string) ([]byte, error) {
				return []byte("garbage"), nil
			},
