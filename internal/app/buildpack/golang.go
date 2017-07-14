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
	return true, "1.22"
}

// GenerateDockerfile generates a Dockerfile for Go applications
func (d *GoDetector) GenerateDockerfile(opts GenerateOptions) (string, error) {
	goVersion := opts.GoVersion
	if goVersion == "" {
		goVersion = "1.22"
	}

	port := opts.Port
	if port == 0 {
		port = 8080
	}

	// Detect main package location
	mainPackage := d.detectMainPackage(opts.Files)

	return fmt.Sprintf(`# Auto-generated Dockerfile for Go
# Build stage
FROM golang:%s-alpine AS builder
