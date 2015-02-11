//go:build !windows && !containarium_client

package cmd

import (
	"errors"
	"testing"
	"time"
)

func TestParseSentinelCandidates(t *testing.T) {
	cands, err := parseSentinelCandidates([]string{"us=us.example.com:443", "plain.example.com:443", " eu = eu.example.com:443 "})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("want 3 candidates, got %d", len(cands))
	}
	if cands[0].Region != "us" || cands[0].Addr != "us.example.com:443" {
		t.Errorf("labeled parse wrong: %+v", cands[0])
	}
	if cands[1].Region != "" || cands[1].Addr != "plain.example.com:443" {
		t.Errorf("bare parse wrong: %+v", cands[1])
	}
