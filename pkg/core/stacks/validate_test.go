package stacks

import (
	"strings"
	"testing"
)

func TestValidateStackID(t *testing.T) {
	m := GetDefault()
	for id, want := range map[string]bool{"": true, "nodejs": true, "no-such-stack": false} {
		if got := m.ValidateStackID(id); got != want {
			t.Errorf("ValidateStackID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestCheckStackID(t *testing.T) {
	m := GetDefault()
