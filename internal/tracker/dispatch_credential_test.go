package tracker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Credential resolution at each use (#2269): the dispatcher resolves the
// connection's broker credential through its CredentialSource at every
// forge call, never once per tick. RunEnded and the sweep can run up to
// the policy's run timeout after the tick that started the run; a
// rotated or short-lived credential resolved at that tick is stale by
// then. Real Postgres store, fake forge that records the credential each
// call carried, a credential source that rotates on every call.

const testCredentialSecret = "forge-token"

// rotatingCredentials returns a new value on every call — every
// resolution is a rotation — and can be rotated from outside (another
// consumer, or the secret being replaced) between calls.
type rotatingCredentials struct {
	mu   sync.Mutex
	n    int
	err  error
	asks []string // "tenant/secret" of every call
}

func (r *rotatingCredentials) BrokerCredential(_ context.Context, tenant, secretName string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asks = append(r.asks, tenant+"/"+secretName)
	if r.err != nil {
		return "", r.err
	}
	r.n++
	return fmt.Sprintf("cred-%d", r.n), nil
}

// rotate replaces the stored credential without anyone resolving it.
func (r *rotatingCredentials) rotate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
}

// current is the only value the forge still accepts.
func (r *rotatingCredentials) current() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("cred-%d", r.n)
}

func (r *rotatingCredentials) setErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *rotatingCredentials) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.asks...)
}

// forgeUse is one forge call and the credential it carried.
type forgeUse struct {
	op, cred, current string
}

// credRecordingProvider records the credential every forge call
// carries, alongside the value that was current at that moment.
type credRecordingProvider struct {
	*fakeDispatchProvider
	src  *rotatingCredentials
	mu   sync.Mutex
	uses []forgeUse
}

func (p *credRecordingProvider) record(op string, c Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.uses = append(p.uses, forgeUse{op: op, cred: c.Credential, current: p.src.current()})
}

func (p *credRecordingProvider) ListIssues(ctx context.Context, c Conn, f IssueFilter) ([]Issue, error) {
	p.record("ListIssues", c)
	return p.fakeDispatchProvider.ListIssues(ctx, c, f)
}

func (p *credRecordingProvider) GetIssue(ctx context.Context, c Conn, n int64) (Issue, error) {
	p.record("GetIssue", c)
	return p.fakeDispatchProvider.GetIssue(ctx, c, n)
}

func (p *credRecordingProvider) SetLabels(ctx context.Context, c Conn, n int64, add, remove []string) error {
	p.record(fmt.Sprintf("SetLabels+%v", add), c)
	return p.fakeDispatchProvider.SetLabels(ctx, c, n, add, remove)
}

func (p *credRecordingProvider) Comment(ctx context.Context, c Conn, n int64, body string) (Comment, error) {
	p.record("Comment", c)
	return p.fakeDispatchProvider.Comment(ctx, c, n, body)
}

// since returns the uses recorded from index i on.
func (p *credRecordingProvider) since(i int) []forgeUse {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]forgeUse(nil), p.uses[i:]...)
}

func (p *credRecordingProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.uses)
}

// credentialHarness builds one Dispatcher per tick, the way
// DispatchTrackerIssues does: the connection record is read and the
// credential resolved when the tick begins, then the dispatcher is
// handed the source to resolve it again at each use.
type credentialHarness struct {
	t        *testing.T
	store    *Store
	provider *credRecordingProvider
	src      *rotatingCredentials
	runs     *lifecycleRunStarter
	clock    *fakeClock
	leases   *fakeLeases
	user     string
}

func newCredentialHarness(t *testing.T, user string, issues ...Issue) (*credentialHarness, context.Context) {
	t.Helper()
	base, store, _, runs, ctx := newLifecycleFixture(t, user)
	src := &rotatingCredentials{}
	h := &credentialHarness{
		t: t, store: store, src: src, runs: runs, user: user,
		provider: &credRecordingProvider{fakeDispatchProvider: newFakeDispatchProvider(issues...), src: src},
		clock:    clockOf(t, base),
		leases:   newFakeLeases(),
	}
	return h, ctx
}

// dispatcher is the per-tick Dispatcher: Conn.Credential is the value
// resolved when the tick began.
func (h *credentialHarness) dispatcher(ctx context.Context) *Dispatcher {
	h.t.Helper()
	cred, err := h.src.BrokerCredential(ctx, h.user, testCredentialSecret)
	if err != nil {
