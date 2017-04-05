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
	if cands[2].Region != "eu" || cands[2].Addr != "eu.example.com:443" {
		t.Errorf("whitespace-trim parse wrong: %+v", cands[2])
	}
}

func TestParseSentinelCandidates_Errors(t *testing.T) {
	for _, in := range [][]string{
		{},            // none
		{"=host:443"}, // empty region
		{"us="},       // empty addr
		{"   "},       // blank → none
	} {
		if _, err := parseSentinelCandidates(in); err == nil {
			t.Errorf("parseSentinelCandidates(%v) expected error", in)
		}
	}
}

func TestPickLowestRTT(t *testing.T) {
	rows := []rttRow{
		{Cand: sentinelCandidate{Region: "us", Addr: "a"}, RTT: 30 * time.Millisecond},
		{Cand: sentinelCandidate{Region: "eu", Addr: "b"}, RTT: 10 * time.Millisecond},
		{Cand: sentinelCandidate{Region: "ap", Addr: "c"}, Err: errors.New("timeout")},
	}
