package tracker

import (
	"errors"
	"testing"
)

func TestNextPageURL(t *testing.T) {
	for _, tc := range []struct {
		name, header, want string
	}{
		{"empty", "", ""},
