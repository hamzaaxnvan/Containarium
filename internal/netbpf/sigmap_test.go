package netbpf

import (
	"encoding/binary"
	"testing"
)

func TestSignatureValueLayout(t *testing.T) {
	var e SignatureEntry
	e.ID = 0x0102
	e.Len = 7
	e.Enabled = true
	copy(e.Pattern[:], []byte("${jndi:"))

	b := signatureValue(e)
	if len(b) != signatureValueSize {
		t.Fatalf("value size = %d, want %d", len(b), signatureValueSize)
	}
	if b[0] != 7 {
		t.Errorf("len byte = %d, want 7", b[0])
	}
	if b[1] != 1 {
		t.Errorf("enabled byte = %d, want 1", b[1])
	}
	if got := binary.NativeEndian.Uint16(b[2:4]); got != 0x0102 {
		t.Errorf("id = %#x, want 0x0102", got)
	}
	if string(b[4:11]) != "${jndi:" {
		t.Errorf("pattern bytes = %q, want ${jndi:", string(b[4:11]))
	}

	// A disabled (empty) slot serializes enabled=0, len=0.
	d := signatureValue(SignatureEntry{})
	if d[0] != 0 || d[1] != 0 {
		t.Errorf("empty slot = len %d enabled %d, want 0/0", d[0], d[1])
	}
