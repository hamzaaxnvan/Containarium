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
