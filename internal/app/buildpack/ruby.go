package buildpack

import (
	"fmt"
)

// RubyDetector detects Ruby applications
type RubyDetector struct{}

// Name returns the language name
func (d *RubyDetector) Name() string {
	return "Ruby"
}

// Detect checks if the project is a Ruby application
func (d *RubyDetector) Detect(files []string) (bool, string) {
	if !containsFile(files, "Gemfile") {
		return false, ""
	}

	// Default version
	return true, "3.3"
}

// GenerateDockerfile generates a Dockerfile for Ruby applications
func (d *RubyDetector) GenerateDockerfile(opts GenerateOptions) (string, error) {
	port := opts.Port
	if port == 0 {
		port = 3000
	}

	// Detect if it's a Rails app
	isRails := d.isRailsApp(opts.Files)

	if isRails {
		return d.generateRailsDockerfile(port), nil
	}

	return d.generateGenericDockerfile(port), nil
