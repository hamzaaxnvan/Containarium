package expose

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClient is a minimal APIClient for unit-testing Run() without a
// real transport. Each test sets the desired LookupContainer response
// and CreateRoute response (or error) up front.
type fakeClient struct {
	// LookupContainer behavior
	lookupName, lookupIP, lookupState string
	lookupErr                         error
	lookupCalls                       int

	// ipAfterCall, when > 0, models a box still coming up: LookupContainer
	// returns an empty IP until the Nth call, then returns lookupIP (the box
	// "got its IP"). Used to test the create→expose wait.
	ipAfterCall int

	// CreateRoute behavior
	createResult *RouteResult
	createErr    error
	createCalls  int
	lastParams   AddRouteParams
}

func (f *fakeClient) LookupContainer(_ context.Context, _ string) (string, string, string, error) {
	f.lookupCalls++
	if f.ipAfterCall > 0 && f.lookupCalls < f.ipAfterCall {
		return f.lookupName, "", f.lookupState, f.lookupErr
	}
	return f.lookupName, f.lookupIP, f.lookupState, f.lookupErr
}

func (f *fakeClient) CreateRoute(_ context.Context, p AddRouteParams) (*RouteResult, error) {
	f.createCalls++
	f.lastParams = p
	return f.createResult, f.createErr
}

func TestRun_HappyPath(t *testing.T) {
	c := &fakeClient{
		lookupName:   "alice-container",
		lookupIP:     "10.0.3.42",
		lookupState:  "Running",
