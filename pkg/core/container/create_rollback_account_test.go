package container

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// accountLog records the host-account operations a Create makes, in order, so
// a test can assert not just that the account was removed but that the removal
// came after the (concurrent) creation finished.
type accountLog struct {
	mu     sync.Mutex
	events []string
}

func (l *accountLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *accountLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func (l *accountLog) joined() string { return strings.Join(l.snapshot(), ",") }

// stubAccountLifecycle replaces every host-side account operation Create makes
// (create, ensure, exists, delete) for the test. existed is what `id <user>`
// reports BEFORE the create starts. Not parallel-safe (package-level seams).
func stubAccountLifecycle(t *testing.T, existed bool, createDelay time.Duration) *accountLog {
	t.Helper()
	log := &accountLog{}
	prevCreate, prevAdd, prevEnsure, prevExists, prevDelete :=
		createJumpServerAccountFn, addAuthorizedKeyFn, ensureJumpServerAccountFn, userExistsFn, deleteJumpServerAccountFn
	createJumpServerAccountFn = func(string, string, bool) error {
		log.add("create:start")
		time.Sleep(createDelay)
		log.add("create:end")
		return nil
	}
	ensureJumpServerAccountFn = func(string) error {
		log.add("ensure:start")
		time.Sleep(createDelay)
		log.add("ensure:end")
		return nil
	}
	addAuthorizedKeyFn = func(string, string) error { return nil }
	userExistsFn = func(string) bool { return existed }
	deleteJumpServerAccountFn = func(user string, _ bool) error {
		log.add("delete:" + user)
		return nil
	}
	t.Cleanup(func() {
		createJumpServerAccountFn, addAuthorizedKeyFn, ensureJumpServerAccountFn, userExistsFn, deleteJumpServerAccountFn =
			prevCreate, prevAdd, prevEnsure, prevExists, prevDelete
	})
	return log
}

// #2850: a create that fails after the host jump account was started must not
// leave that account behind, and must wait for the concurrent creation to
// finish first (otherwise the account is made after the removal).
func TestCreate_FailureAfterTheAccountStartedRemovesTheAccountItMade(t *testing.T) {
	for _, tc := range []struct {
		name string
