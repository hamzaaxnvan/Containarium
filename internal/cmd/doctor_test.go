//go:build !windows && !containarium_client

package cmd

import (
	"testing"

	"github.com/footprintai/containarium/internal/hostcheck"
)

func TestPrintDoctor_CountsRequiredFailures(t *testing.T) {
	checks := []hostcheck.Check{
		{Name: "ok-required", OK: true, Required: true},
		{Name: "fail-required", OK: false, Required: true, Detail: "x"}, // counts
