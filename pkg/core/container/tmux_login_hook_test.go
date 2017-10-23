package container

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
)

// provisionRecorder fakes the calls installPackages makes: it records every
// Exec argv and every WriteFile, and can fail WriteFile on demand.
type provisionRecorder struct {
	incus.Backend
	execs        [][]string
	writes       map[string][]byte
	modes        map[string]string
	writeFileErr error
	failPath     string // when set, only a WriteFile to this path returns writeFileErr
	moshExecErr  error  // returned by the separate mosh install script (RHEL/EPEL)
}

func (b *provisionRecorder) ExecWithExitCode(_ string, _ []string) (string, string, int, error) {
	return "", "", 1, nil // no cloud-init: skip the wait
}

func (b *provisionRecorder) Exec(_ string, command []string) error {
	b.execs = append(b.execs, command)
	if isMoshInstall(command) {
		return b.moshExecErr
	}
	return nil
}

// isMoshInstall reports whether command is the separate best-effort mosh
// install script (bash -c '... install -y mosh ...').
func isMoshInstall(c []string) bool {
	return len(c) == 3 && c[0] == "bash" && strings.Contains(c[2], "install -y mosh")
}

func (b *provisionRecorder) WriteFile(_ string, path string, content []byte, mode string) error {
	if b.writeFileErr != nil && (b.failPath == "" || b.failPath == path) {
		return b.writeFileErr
	}
	if b.writes == nil {
		b.writes, b.modes = map[string][]byte{}, map[string]string{}
	}
	b.writes[path], b.modes[path] = content, mode
	return nil
}

// installArgv returns the package-install argv (apt-get/dnf install -y ...).
func (b *provisionRecorder) installArgv(t *testing.T) []string {
	t.Helper()
	for _, c := range b.execs {
		if len(c) > 2 && (c[0] == "apt-get" || c[0] == "dnf") && c[1] == "install" && slices.Contains(c, "openssh-server") {
			return c
		}
	}
	t.Fatalf("no base package install among execs: %v", b.execs)
	return nil
}

func TestInstallPackages_InstallsTmuxMoshAndLoginHook(t *testing.T) {
	tests := []struct {
		name         string
		family       ostype.OSFamily
		wantPackages []string
		wantMoshExec bool // mosh via the separate best-effort script (EPEL)
	}{
		{"debian", ostype.Debian, []string{"tmux", "mosh"}, false},
		{"rhel", ostype.RHEL, []string{"tmux"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &provisionRecorder{}
			if err := NewWithBackend(b).installPackages("box-container", false, "", nil, "", tt.family); err != nil {
				t.Fatalf("installPackages: %v", err)
			}
			argv := b.installArgv(t)
			for _, p := range tt.wantPackages {
				if !slices.Contains(argv, p) {
					t.Errorf("package install %v is missing %q", argv, p)
				}
			}
			ranMosh := slices.ContainsFunc(b.execs, isMoshInstall)
			if ranMosh != tt.wantMoshExec {
				t.Errorf("separate mosh install ran = %v, want %v", ranMosh, tt.wantMoshExec)
			}
			got, ok := b.writes[tmuxLoginHookPath]
			if !ok {
				t.Fatalf("login hook not written to %s; writes: %v", tmuxLoginHookPath, b.writes)
			}
			if !bytes.Equal(got, tmuxLoginHook) {
				t.Errorf("login hook content differs from the embedded tmux_login_hook.sh")
			}
			if b.modes[tmuxLoginHookPath] != "0644" {
				t.Errorf("login hook mode = %q, want 0644", b.modes[tmuxLoginHookPath])
			}
		})
	}
}

// A box without the hook is still a working box: a failed write must not
// fail provisioning.
func TestInstallPackages_LoginHookWriteFailureIsNotFatal(t *testing.T) {
	b := &provisionRecorder{writeFileErr: errors.New("push failed"), failPath: tmuxLoginHookPath}
	if err := NewWithBackend(b).installPackages("box-container", false, "", nil, "", ostype.Debian); err != nil {
