//go:build !containarium_client

package cmd

import "testing"

func TestParseForwardMap(t *testing.T) {
	t.Run("empty is nil", func(t *testing.T) {
		m, err := parseForwardMap(nil)
		if err != nil || m != nil {
			t.Fatalf("parseForwardMap(nil) = (%v, %v), want (nil, nil)", m, err)
		}
