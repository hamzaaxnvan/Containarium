package cmd

import "testing"

func TestParseSizeBytes(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"50GB", 50_000_000_000, false},
		{"1TB", 1_000_000_000_000, false},
		{"500MB", 500_000_000, false},
		{"1GiB", 1 << 30, false},
		{"2TiB", 2 << 40, false},
