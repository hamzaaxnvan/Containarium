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
