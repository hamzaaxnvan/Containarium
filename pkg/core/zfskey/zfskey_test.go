package zfskey

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"testing"
)

func mustKey(t *testing.T, b []byte) Key {
	t.Helper()
	k, err := NewKey(b)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func testKeyBytes(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, KeyLen)
}

// AC: key material never appears in logs, errors, or any String()/
// marshalled form.
//
// This is asserted rather than reviewed because every one of these is a
// path key bytes have escaped through in real systems: a %v in a log
// line, a %#v in a debug dump, a struct marshalled into an API response.
