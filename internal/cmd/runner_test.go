package cmd

import (
	"strings"
	"testing"
)

// TestRunRunnerProvision_RunnerGroupFlag checks --runner-group is registered
// and reaches runner.ValidateOptions: a group on a repository target is
// refused before the sentinel check or any box work.
func TestRunRunnerProvision_RunnerGroupFlag(t *testing.T) {
	if runnerProvisionCmd.Flags().Lookup("runner-group") == nil {
		t.Fatal("runner provision has no --runner-group flag")
	}

	prevPAT, prevCount, prevGroup, prevSentinel := runnerPAT, runnerCount, runnerGroup, runnerSentinelHost
	t.Cleanup(func() {
