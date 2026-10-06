package incus

import (
	"errors"
	"testing"
	"time"
)

// TestUnavailableBackend_ReturnsErrUnavailable — a representative sample of the
// Backend surface returns ErrUnavailable rather than panicking, so a daemon on
// a host without incus can still construct a Manager and start.
func TestUnavailableBackend_ReturnsErrUnavailable(t *testing.T) {
	b := NewUnavailableBackend()

	if err := b.StartContainer("x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("StartContainer err = %v, want ErrUnavailable", err)
	}
	if _, err := b.GetContainer("x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("GetContainer err = %v, want ErrUnavailable", err)
	}
	if _, err := b.ListContainers(); !errors.Is(err, ErrUnavailable) {
		t.Errorf("ListContainers err = %v, want ErrUnavailable", err)
	}
	if _, err := b.WaitForNetwork("x", time.Second); !errors.Is(err, ErrUnavailable) {
		t.Errorf("WaitForNetwork err = %v, want ErrUnavailable", err)
	}
	if _, _, err := b.ExecWithOutput("x", []string{"true"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("ExecWithOutput err = %v, want ErrUnavailable", err)
	}
}
