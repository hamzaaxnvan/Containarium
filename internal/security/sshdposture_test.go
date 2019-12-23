package security

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	"github.com/footprintai/containarium/pkg/core/sshdpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeBox is one box's filesystem + incus config as the reconciler sees it
// through the Backend: files by absolute path, drop-in dir entries derived
// from them, and every write/exec/config-set recorded.
type fakeBox struct {
	files  map[string]string
	config map[string]string

	writes    map[string]string
	execs     []string
	setCfg    map[string]string
	writeErr  error
	setCfgErr error // SetConfig fails (the marker cannot be stamped)
}

func newFakeBox(files map[string]string, marked bool) *fakeBox {
	b := &fakeBox{files: map[string]string{}, config: map[string]string{}, writes: map[string]string{}, setCfg: map[string]string{}}
	for k, v := range files {
		b.files[k] = v
	}
	if marked {
		b.config[sshdpolicy.MarkerKey] = sshdpolicy.MarkerValue
	}
	return b
}

func (b *fakeBox) backend() *incustest.MockBackend {
	m := incustest.NewMockBackend()
	m.ReadFileFunc = func(_, path string) ([]byte, error) {
		c, ok := b.files[path]
		if !ok {
			return nil, errors.New("not found")
		}
		return []byte(c), nil
	}
	m.ListDirFunc = func(_, path string) ([]string, error) {
		var out []string
		for p := range b.files {
			if strings.HasPrefix(p, path+"/") {
				out = append(out, strings.TrimPrefix(p, path+"/"))
			}
		}
		if len(out) == 0 {
			return nil, errors.New("no such directory")
		}
		return out, nil
	}
	m.WriteFileFunc = func(_, path string, content []byte, _ string) error {
		if b.writeErr != nil {
			return b.writeErr
		}
		b.writes[path] = string(content)
		b.files[path] = string(content)
		return nil
	}
	m.ExecFunc = func(_ string, cmd []string) error {
		b.execs = append(b.execs, strings.Join(cmd, " "))
		return nil
	}
	m.GetRawInstanceFunc = func(string) (map[string]string, string, error) { return b.config, "", nil }
	m.SetConfigFunc = func(_, k, v string) error {
		if b.setCfgErr != nil {
			return b.setCfgErr
		}
		b.setCfg[k] = v
		b.config[k] = v
		return nil
	}
	return m
}

type fakeSink struct{ findings []*threatdetect.Finding }

func (s *fakeSink) Upsert(_ context.Context, f *threatdetect.Finding) (*threatdetect.Finding, error) {
	s.findings = append(s.findings, f)
	return f, nil
}

const imageDefaultMain = "Include /etc/ssh/sshd_config.d/*.conf\nPort 22\n"

var managed = string(sshdpolicy.DropInContent())

func TestReconcileBox_ImageDefaultIsBackfilledSilently(t *testing.T) {
	box := newFakeBox(map[string]string{sshdpolicy.MainConfigPath: imageDefaultMain}, false)
	sink := &fakeSink{}
	r := NewSSHDPostureReconciler(box.backend(), sink, 0)

	out, err := r.ReconcileBox(context.Background(), "alice-container")
	if err != nil {
		t.Fatal(err)
	}
	if !out.Backfilled || !out.Remediated || !out.Compliant || out.Finding != nil {
		t.Fatalf("want silent backfill, got %+v", out)
	}
	if box.writes[sshdpolicy.DropInPath] != managed {
		t.Errorf("managed drop-in not written: %q", box.writes)
	}
	if !containsPrefix(box.execs, "sh -c systemctl reload") {
		t.Errorf("sshd not reloaded after write: %v", box.execs)
	}
	if box.setCfg[sshdpolicy.MarkerKey] != sshdpolicy.MarkerValue {
		t.Errorf("marker not set: %v", box.setCfg)
	}
	if len(sink.findings) != 0 {
		t.Errorf("a never-managed box must not raise a finding on backfill: %+v", sink.findings)
	}
}

