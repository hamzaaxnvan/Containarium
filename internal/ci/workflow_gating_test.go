// Package ci holds guards over this repo's CI configuration itself.
//
// The workflows decide whether anything else in the repo is checked at all,
// so a mistake in them is invisible in exactly the way a mistake in normal
// code is not: nothing goes red, a check simply stops existing.
package ci

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const workflowDir = "../../.github/workflows"

// workflowFile models only the trigger section — the rest of a workflow is
// not this test's business.
//
// `on` is decoded as raw nodes rather than a struct because the distinction
// this whole file turns on is *key present but empty* vs *key absent*:
//
//	pull_request:              # present, unfiltered — gates every PR
//	pull_request:              # present, filtered — gates only some
//	  branches: [main]
//	(no pull_request key)      # absent — gates nothing
//
// A bare `pull_request:` is YAML null, so decoding into a *struct pointer
// yields nil and makes the first case indistinguishable from the third. The
// first draft of this test did exactly that and passed vacuously for the
// three workflows it was written to protect.
type workflowFile struct {
	Name string               `yaml:"name"`
	On   map[string]yaml.Node `yaml:"on"`
}

type triggerFilter struct {
	Branches []string `yaml:"branches"`
	Paths    []string `yaml:"paths"`
	Tags     []string `yaml:"tags"`
	Types    []string `yaml:"types"`
