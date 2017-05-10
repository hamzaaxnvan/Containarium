package cmd

import (
	"bytes"
	"testing"
)

func TestDetachEscape_FiresOnCtrlAThenQ(t *testing.T) {
	src := bytes.NewReader([]byte("hello\x01qworld"))
	d, detach := newDetachEscape(src)

	buf := make([]byte, 64)
	n, err := d.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len("hello\x01qworld") {
		t.Fatalf("expected to read all bytes in one call, got %d", n)
	}

	select {
	case <-detach:
