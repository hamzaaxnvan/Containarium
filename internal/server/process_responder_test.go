package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lxc/incus/v7/shared/api"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeActuator is the containerActuator test double: an in-memory
// limits.cpu / limits.cpu.allowance pair and a frozen flag per container,
// plus a scripted failure.
type fakeActuator struct {
	mu     sync.Mutex
	count  map[string]string
	allow  map[string]string
	frozen map[string]bool
	// ids is each box's Incus instance id (volatile.uuid); a box with none
	// set reads as "id-<name>". A box listed in missing answers not found.
	ids     map[string]string
	missing map[string]bool
	fail    error
	calls   []string
}

func (a *fakeActuator) InstanceID(name string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.missing[name] {
		return "", api.StatusErrorf(http.StatusNotFound, "Instance not found")
	}
	if id, ok := a.ids[name]; ok {
		return id, nil
	}
	return "id-" + name, nil
}

func newFakeActuator() *fakeActuator {
	return &fakeActuator{count: map[string]string{}, allow: map[string]string{}, frozen: map[string]bool{},
		ids: map[string]string{}, missing: map[string]bool{}}
}

func (a *fakeActuator) CPULimit(name string) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "CPULimit:"+name)
	if a.fail != nil {
		return "", "", a.fail
	}
	return a.count[name], a.allow[name], nil
}

func (a *fakeActuator) SetCPULimit(name, count, allowance string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "SetCPULimit:"+name+":"+count+":"+allowance)
	if a.fail != nil {
		return a.fail
	}
	a.count[name], a.allow[name] = count, allowance
	return nil
}

func (a *fakeActuator) Freeze(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "Freeze:"+name)
	if a.fail != nil {
		return a.fail
	}
	a.frozen[name] = true
	return nil
}

func (a *fakeActuator) Unfreeze(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "Unfreeze:"+name)
	if a.fail != nil {
		return a.fail
	}
	a.frozen[name] = false
	return nil
}

// fakeFindings is an in-memory findingLifecycle: enough of a finding store
// for the responder (Get/Resolve/SetResponse/Upsert) without Postgres or
// the audit chain that MemFindingStore needs.
type fakeFindings struct {
	// beforeGet is a test-only state-change hook; set before starting a job.
	beforeGet func()
	mu        sync.Mutex
	nextID    int64
	rows      map[int64]*threatdetect.Finding
	// writes is every response handed to SetResponse, as written (before
	// the sticky process_killed merge), so a test can tell what a path
	// recorded from what the store kept.
	writes []threatdetect.FindingResponse
	// held is every box record SetResponseHeld delivered without storing
	// (#2820); resolvedWith every response ResolveWithResponse resolved with.
	held, resolvedWith []threatdetect.FindingResponse
	// notifier, when set, gets what each write would deliver, as the real
	// stores hand it (threatdetect.ResponseDelivery), so a test can run the
	// real webhook gate behind the responder.
	notifier threatdetect.Notifier
}

// notify hands f to the notifier. Called with s.mu held, unlike the real
// stores; the WebhookNotifier never calls back into the store.
func (s *fakeFindings) notify(f *threatdetect.Finding) {
	if s.notifier != nil {
		s.notifier.Notify(f)
	}
}

func newFakeFindings() *fakeFindings { return &fakeFindings{rows: map[int64]*threatdetect.Finding{}} }

func (s *fakeFindings) Upsert(_ context.Context, f *threatdetect.Finding) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.State == threatdetect.FindingStateOpen && r.Rule == f.Rule && r.TenantID == f.TenantID && r.Subject == f.Subject {
			r.Count++
			cp := *r // a real store returns a fresh row, never its own
			if cp.Response != nil {
				resp := *cp.Response
				cp.Response = &resp
			}
			delivered := cp
			delivered.Container = f.Container // the Postgres store returns the writer's box
			defer s.notify(&delivered)
			return &cp, nil
		}
	}
	s.nextID++
	f.ID = s.nextID
	f.State = threatdetect.FindingStateOpen
	f.Count = 1
	s.rows[f.ID] = f
	cp := *f
	defer s.notify(&cp)
	return &cp, nil
}

func (s *fakeFindings) Get(_ context.Context, id int64) (*threatdetect.Finding, error) {
	if s.beforeGet != nil {
		s.beforeGet()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.rows[id]; ok {
		cp := *f
		return &cp, nil
	}
	return nil, threatdetect.ErrFindingNotFound
}

func (s *fakeFindings) Resolve(_ context.Context, id int64) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.rows[id]
	if !ok {
		return nil, threatdetect.ErrFindingNotFound
	}
	if f.State != threatdetect.FindingStateOpen {
		return nil, threatdetect.ErrFindingNotOpen
	}
	f.State = threatdetect.FindingStateResolved
	cp := *f
	defer s.notify(&cp)
	return f, nil
}

func (s *fakeFindings) SetResponse(_ context.Context, id int64, r threatdetect.FindingResponse) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setResponseLocked(id, r, false)
}

// setResponseLocked is SetResponse with s.mu held; resolve also resolves an
// open finding in the same write, delivered once as the resolve.
func (s *fakeFindings) setResponseLocked(id int64, r threatdetect.FindingResponse, resolve bool) (*threatdetect.Finding, error) {
	f, ok := s.rows[id]
	if !ok {
		return nil, threatdetect.ErrFindingNotFound
	}
