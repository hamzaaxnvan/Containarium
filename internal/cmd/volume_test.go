package cmd

import "testing"

func TestParseSizeBytes(t *testing.T) {
	cases := []struct {
		in      string
