package anonbox

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lxc/incus/v7/shared/api"
	"golang.org/x/crypto/ssh"

	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// --- fakes -----------------------------------------------------------------

type writtenFile struct {
	path, mode string
	content    string
}

// fakeBoxes records every call Ensure makes against the box backend and
// serves List from an in-memory table so reuse/expiry paths are testable
// without Incus.
type fakeBoxes struct {
	boxes     []box.BoxStatus
	createErr error

	created   []box.BoxSpec
	deleted   []box.BoxRef
	ttls      map[string]time.Time
	files     map[string][]writtenFile
	execs     [][]string
	execErr   error               // returned by every Exec when set (#2202)
	catOutput map[string]string   // stdout for `cat <path>` execs (#2206); missing path = error
	owners    map[string]string   // SetOwner, by box name
	keys      map[string][]string // SetAuthorizedKeys, by box name
	writes    []box.BoxRef        // every ref a claim-path write was addressed to
}

func newFakeBoxes() *fakeBoxes {
	return &fakeBoxes{ttls: map[string]time.Time{}, files: map[string][]writtenFile{}, owners: map[string]string{}, keys: map[string][]string{}}
}

func (f *fakeBoxes) Create(_ context.Context, spec box.BoxSpec) (*box.BoxStatus, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, spec)
	st := box.BoxStatus{
		Ref:       box.BoxRef{Tenant: spec.Ref.Tenant, Name: spec.Ref.Tenant + "-container"},
		State:     pb.ContainerState_CONTAINER_STATE_RUNNING,
		IPAddress: "10.100.0.57",
		Labels:    spec.Labels,
		Isolation: spec.Isolation,
	}
	f.boxes = append(f.boxes, st)
	return &st, nil
}

func (f *fakeBoxes) Delete(_ context.Context, ref box.BoxRef, _ bool) error {
	f.deleted = append(f.deleted, ref)
	kept := f.boxes[:0]
	for _, b := range f.boxes {
		if b.Ref.Name != ref.Name {
			kept = append(kept, b)
		}
	}
	f.boxes = kept
	return nil
}

func (f *fakeBoxes) List(_ context.Context) ([]box.BoxStatus, error) {
	out := make([]box.BoxStatus, len(f.boxes))
	copy(out, f.boxes)
	for i := range out {
		if exp, ok := f.ttls[out[i].Ref.Name]; ok {
			out[i].TTLExpiresAt = exp
		} else {
			out[i].TTLExpiresAt = time.Time{}
		}
		if owner, ok := f.owners[out[i].Ref.Name]; ok {
			out[i].Ref.Tenant = owner
		}
	}
	return out, nil
}

func (f *fakeBoxes) SetTTL(_ context.Context, ref box.BoxRef, at *time.Time) error {
	if at == nil {
		delete(f.ttls, ref.Name)
		return nil
	}
	f.ttls[ref.Name] = *at
	return nil
}

func (f *fakeBoxes) Exec(_ context.Context, _ box.BoxRef, cmd []string) (string, string, error) {
	f.execs = append(f.execs, cmd)
	if f.execErr != nil {
		return "", "agent unavailable", f.execErr
	}
	if len(cmd) == 2 && cmd[0] == "cat" {
		if out, ok := f.catOutput[cmd[1]]; ok {
			return out, "", nil
		}
		return "", "No such file or directory", errors.New("exit 1")
	}
	return "", "", nil
}

func (f *fakeBoxes) WriteFile(_ context.Context, ref box.BoxRef, path string, content []byte, mode string) error {
	f.files[ref.Name] = append(f.files[ref.Name], writtenFile{path: path, mode: mode, content: string(content)})
	return nil
}

// fakeACLs is the Incus ACL/NIC slice.
type fakeACLs struct {
	acls      map[string]incus.ACLConfig
	nics      map[string]incus.NICDevice
	devKeys   map[string]map[string]string
	createErr error
}

func newFakeACLs() *fakeACLs {
	return &fakeACLs{acls: map[string]incus.ACLConfig{}, nics: map[string]incus.NICDevice{}, devKeys: map[string]map[string]string{}}
}

func (f *fakeACLs) GetNetworkACL(name string) (*api.NetworkACL, error) {
	if _, ok := f.acls[name]; !ok {
		return nil, errors.New("not found")
	}
	acl := &api.NetworkACL{}
	acl.Name = name
	return acl, nil
}
func (f *fakeACLs) CreateNetworkACL(c incus.ACLConfig) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.acls[c.Name] = c
	return nil
}
func (f *fakeACLs) UpdateNetworkACL(name string, c incus.ACLConfig) error {
	f.acls[name] = c
	return nil
}
func (f *fakeACLs) EnsureNICDevice(name string, want incus.NICDevice) error {
	f.nics[name] = want
	return nil
}
func (f *fakeACLs) SetDeviceConfig(name, dev string, keys map[string]string) error {
	f.devKeys[name+"/"+dev] = keys
	return nil
}

// --- helpers ---------------------------------------------------------------

func testKey(t *testing.T) (authorizedKey, fingerprint string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
