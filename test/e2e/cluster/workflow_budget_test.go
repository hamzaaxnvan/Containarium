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
