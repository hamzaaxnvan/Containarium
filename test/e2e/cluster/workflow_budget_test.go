package clustere2e

// The lane's own timing arithmetic, asserted rather than described
// (#1458).
//
// Run 12 ran for 1h40m and was cancelled at the job's timeout-minutes.
// That is the worst way for a lane to end: a cancelled job never
// reaches the "Diagnostics on failure" step or the suite's own
// dumpDiagnostics, so 100 minutes of runner time produced no evidence
// at all — indistinguishable, from the outside, from a lane that is
// merely slow.
//
// The relationship that has to hold is:
//
//	sum(step budgets) <= go timeout < job cap - setup
//
// A comment saying so relies on the next person changing a budget
// happening to read it. This does not.

import (
	"fmt"
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const containerLaneWorkflow = "../../../.github/workflows/cluster-container-e2e.yml"
const kvmLaneWorkflow = "../../../.github/workflows/cluster-e2e.yml"

// laneSetupBudget is the runner provisioning the job does before the
// suite starts: Incus install, ZFS pool, build, Postgres image pull.
// Measured at ~10m; held at 15m so the assertion has margin of its own
// rather than tracking the fastest run ever observed.
const laneSetupBudget = 15 * time.Minute

type workflowFile struct {
	Jobs map[string]struct {
		TimeoutMinutes int               `yaml:"timeout-minutes"`
		Env            map[string]string `yaml:"env"`
	} `yaml:"jobs"`
}

func TestContainerLaneJobCapExceedsItsTestBudget(t *testing.T) {
	raw, err := os.ReadFile(containerLaneWorkflow)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	var wf workflowFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}

	// EVERY job that runs the suite, not just the happy-path one.
	// Checking only container-e2e would leave the identical latent
	// bug sitting in the job next to it — and the sabotage jobs are
	// the ones whose output matters most when they do time out,
	// because a cancelled proof run reports "cannot fail" for a lane
	// that was merely slow.
	checked := 0
	for name, job := range wf.Jobs {
		if _, runsSuite := job.Env["CONTAINARIUM_E2E_GO_TIMEOUT"]; !runsSuite {
			continue
		}
		checked++
		t.Run(name, func(t *testing.T) {
			if job.TimeoutMinutes == 0 {
				t.Fatal("no timeout-minutes, so a hung run burns a whole runner")
			}
			jobCap := time.Duration(job.TimeoutMinutes) * time.Minute
			goTimeout := mustDuration(t, job.Env, "CONTAINARIUM_E2E_GO_TIMEOUT")

			// Step budgets are optional per job: the sabotage jobs
			// tighten only what they need. Sum whichever are present.
			var stepTotal time.Duration
			var present int
			for _, key := range []string{
				"CONTAINARIUM_E2E_READY_TIMEOUT",
				"CONTAINARIUM_E2E_SCALEUP_TIMEOUT",
				"CONTAINARIUM_E2E_VPA_TIMEOUT",
				"CONTAINARIUM_E2E_SCALEDOWN_TIMEOUT",
				"CONTAINARIUM_E2E_DELETE_TIMEOUT",
			} {
				if _, ok := job.Env[key]; !ok {
