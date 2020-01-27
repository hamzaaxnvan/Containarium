package server

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mintCounter returns a mint func that hands out "tok-1", "tok-2", … and counts
// calls, so a test can prove exactly when a re-mint happened.
func mintCounter(n *int) func() (string, error) {
	return func() (string, error) {
		*n++
		return fmt.Sprintf("tok-%d", *n), nil
	}
}

func TestServiceTokenSource_CachesAndRenews(t *testing.T) {
	var mints int
	now := time.Unix(0, 0)
	s := newServiceTokenSource(mintCounter(&mints), time.Hour, 10*time.Minute)
	s.now = func() time.Time { return now }
