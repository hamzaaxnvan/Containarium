package stacks

import (
	"embed"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed *.yaml
var embeddedFS embed.FS

// StackParameter describes an input the stack accepts at install time.
// Parameters are surfaced in the web UI's Create Container dialog and forwarded
// to install scripts as environment variables (CONTAINARIUM_STACK_<NAME>).
type StackParameter struct {
	// Name is the parameter key (e.g., "kubeflow_user"). The env var seen by
	// install scripts is CONTAINARIUM_STACK_<NAME>.
	Name string `yaml:"name" json:"name"`
	// Label is the human-readable label shown in the web UI form.
	Label string `yaml:"label" json:"label"`
	// Description is optional helper text shown below the form field.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Type is "string", "password", "number", or "boolean". Controls how the
	// web UI renders the field; the value is always sent as a string.
	Type string `yaml:"type" json:"type"`
	// Default is the fallback if the user leaves the field blank.
	Default string `yaml:"default,omitempty" json:"default,omitempty"`
	// Required marks the parameter as mandatory in the web UI form.
	Required bool `yaml:"required,omitempty" json:"required,omitempty"`
}

// Stack represents a pre-configured software stack
type Stack struct {
	ID          string           `yaml:"id" json:"id"`
	Name        string           `yaml:"name" json:"name"`
	Description string           `yaml:"description" json:"description"`
	Icon        string           `yaml:"icon" json:"icon"`
	PreInstall  []string         `yaml:"pre_install" json:"preInstall"`
	Packages    []string         `yaml:"packages" json:"packages"`
	PostInstall []string         `yaml:"post_install" json:"postInstall"`
	Parameters  []StackParameter `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	// RHEL-specific overrides (optional; falls back to default fields if empty)
	RHELPreInstall  []string `yaml:"rhel_pre_install,omitempty" json:"rhelPreInstall,omitempty"`
	RHELPackages    []string `yaml:"rhel_packages,omitempty" json:"rhelPackages,omitempty"`
	RHELPostInstall []string `yaml:"rhel_post_install,omitempty" json:"rhelPostInstall,omitempty"`
}

// GetPreInstallForFamily returns the pre-install commands for the given OS family.
func (s *Stack) GetPreInstallForFamily(family string) []string {
	if family == "rhel" && len(s.RHELPreInstall) > 0 {
		return s.RHELPreInstall
	}
	return s.PreInstall
}

// GetPackagesForFamily returns the packages for the given OS family.
func (s *Stack) GetPackagesForFamily(family string) []string {
	if family == "rhel" && len(s.RHELPackages) > 0 {
		return s.RHELPackages
	}
	return s.Packages
}

// GetPostInstallForFamily returns the post-install commands for the given OS family.
func (s *Stack) GetPostInstallForFamily(family string) []string {
	if family == "rhel" && len(s.RHELPostInstall) > 0 {
		return s.RHELPostInstall
	}
	return s.PostInstall
}

// Config holds the stack configuration
type Config struct {
	BaseScripts []Stack `yaml:"base_scripts" json:"baseScripts"`
	Stacks      []Stack `yaml:"stacks" json:"stacks"`
}

// Manager manages stack definitions
type Manager struct {
	config Config
	mu     sync.RWMutex
}

var (
	defaultManager *Manager
	once           sync.Once
)

// DefaultConfigPaths are the default locations to search for stacks.yaml,
