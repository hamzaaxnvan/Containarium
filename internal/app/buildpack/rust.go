package buildpack

import (
	"fmt"
)

// RustDetector detects Rust applications
type RustDetector struct{}

// Name returns the language name
func (d *RustDetector) Name() string {
	return "Rust"
}

// Detect checks if the project is a Rust application
func (d *RustDetector) Detect(files []string) (bool, string) {
	if !containsFile(files, "Cargo.toml") {
		return false, ""
	}

	// Default version
