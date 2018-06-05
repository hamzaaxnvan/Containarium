// Package proofpack generates a Proof Pack: the repository's declared test
// commands, run by the platform inside a box, with each command's exit
// status and output recorded and digested (docs/PROOF-PACK.md, #2797).
//
// The coding agent has no input into a pack. Generate runs the commands
// itself through an Execer (the daemon's Incus backend) and records what
// happened; a `tests_run` claim from the agent is never consulted.
package proofpack

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// DeclarationPath is where a repository declares its test command set,
// relative to the repository root.
const DeclarationPath = ".github/containarium.yml"

const (
	// MaxDeclarationBytes bounds the declaration file read from the box.
	MaxDeclarationBytes = 64 * 1024
	// MaxSteps bounds how many steps one declaration may list.
	MaxSteps = 32
	// fallbackStepName names the `make test` step used when nothing is
	// declared.
	fallbackStepName = "make-test"
	fallbackCommand  = "make test"
	// stringFormStepName names the single step of a `test: <command>`
	// declaration.
	stringFormStepName = "test"
)

// stepNamePattern keeps step names short and free of anything that reads
// like a command line, since the name is what a pack records instead of one.
var stepNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// kindNames maps the declaration's `kind:` values to the proto enum.
var kindNames = map[string]pb.ProofPartKind{
	"test":            pb.ProofPartKind_PROOF_PART_KIND_TEST,
	"unit":            pb.ProofPartKind_PROOF_PART_KIND_UNIT,
	"integration":     pb.ProofPartKind_PROOF_PART_KIND_INTEGRATION,
	"e2e":             pb.ProofPartKind_PROOF_PART_KIND_E2E,
	"static_analysis": pb.ProofPartKind_PROOF_PART_KIND_STATIC_ANALYSIS,
	"security_scan":   pb.ProofPartKind_PROOF_PART_KIND_SECURITY_SCAN,
}

// Step is one command to run. Run is the command line: it is executed but
// never copied into a pack.
type Step struct {
	Name string
	Kind pb.ProofPartKind
	Run  string
}

// declarationFile is the part of .github/containarium.yml this package
// reads. Other top-level keys belong to other features and are ignored.
// Test is a raw node so a present-but-null `test:` (Kind set, tag !!null)
// is told apart from an absent key (Kind 0): yaml.v3 does not call a
// custom unmarshaler for a null value.
type declarationFile struct {
	Test yaml.Node `yaml:"test"`
}

type stepYAML struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
	Run  string `yaml:"run"`
}

// requireStrings rejects a step field whose value is not a YAML string
// (a bool or int that yaml.v3 would coerce), naming the line.
func requireStrings(n *yaml.Node) error {
