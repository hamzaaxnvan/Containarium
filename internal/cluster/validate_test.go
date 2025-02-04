package cluster

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		ok    bool
	}{
		{"simple", "demo", true},
		{"single char", "a", true},
		{"hyphenated", "web-1", true},
		{"max length 20", strings.Repeat("a", 20), true},
		{"empty", "", false},
		{"too long 21", strings.Repeat("a", 21), false},
		{"leading hyphen", "-a", false},
		{"trailing hyphen", "a-", false},
		{"uppercase", "Demo", false},
		{"underscore", "a_b", false},
		{"dot", "a.b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.input)
			if tc.ok && err != nil {
