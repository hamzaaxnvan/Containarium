package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Scorecard publishes from a privileged job. These guards prevent a later
// workflow edit from exposing its token to PR code or invalidating publication.
type scorecardWorkflow struct {
	On          map[string]yaml.Node    `yaml:"on"`
	Permissions map[string]string       `yaml:"permissions"`
	Env         yaml.Node               `yaml:"env"`
	Defaults    yaml.Node               `yaml:"defaults"`
	Jobs        map[string]scorecardJob `yaml:"jobs"`
}

type scorecardJob struct {
	RunsOn      string            `yaml:"runs-on"`
	Permissions map[string]string `yaml:"permissions"`
	Env         yaml.Node         `yaml:"env"`
	Defaults    yaml.Node         `yaml:"defaults"`
	Container   yaml.Node         `yaml:"container"`
	Services    yaml.Node         `yaml:"services"`
	Steps       []scorecardStep   `yaml:"steps"`
}

type scorecardStep struct {
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

func loadScorecardWorkflow(t *testing.T) scorecardWorkflow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(workflowDir, "scorecard.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf scorecardWorkflow
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestScorecardTrustedTriggers(t *testing.T) {
	wf := loadScorecardWorkflow(t)
	if len(wf.On) != 2 {
		t.Fatal("Scorecard must run only on main pushes and the weekly schedule")
	}
	var push triggerFilter
	pushNode := wf.On["push"]
	if err := pushNode.Decode(&push); err != nil {
		t.Fatal(err)
	}
	if len(push.Branches) != 1 || push.Branches[0] != "main" || len(push.Paths) != 0 || len(push.Tags) != 0 {
		t.Error("every main push must run Scorecard, without path or tag filters")
	}
	var schedule []struct {
		Cron string `yaml:"cron"`
	}
	scheduleNode := wf.On["schedule"]
	if err := scheduleNode.Decode(&schedule); err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 1 || !regexp.MustCompile(`^[0-5]?\d [0-2]?\d \* \* [0-6]$`).MatchString(schedule[0].Cron) {
		t.Error("Scorecard must have a weekly schedule")
	}
}

func TestScorecardBadge(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
