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
	default:
		t.Fatal("expected detach channel to be closed after <ctrl>+a q")
	}
}

func TestDetachEscape_DoesNotFireOnQAlone(t *testing.T) {
	src := bytes.NewReader([]byte("just q with no ctrl-a"))
	d, detach := newDetachEscape(src)

	buf := make([]byte, 64)
	if _, err := d.Read(buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case <-detach:
		t.Fatal("detach fired without the <ctrl>+a prefix")
	default:
	}
}

func TestDetachEscape_CtrlAThenOtherKeyDoesNotArmAcrossReads(t *testing.T) {
	// <ctrl>+a followed by something other than 'q' should not leave the
