package buildpack

import (
	"fmt"
)

// GoDetector detects Go applications
type GoDetector struct{}

// Name returns the language name
func (d *GoDetector) Name() string {
	return "Go"
}

// Detect checks if the project is a Go application
func (d *GoDetector) Detect(files []string) (bool, string) {
	if !containsFile(files, "go.mod") {
		return false, ""
	}

	// Default version - would normally parse go.mod
