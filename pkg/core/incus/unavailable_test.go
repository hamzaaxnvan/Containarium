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
