package buildpack

import (
	"fmt"
	"strings"
)

// defaultNodeVersion is the Node.js major used when the app does not pick one.
// Node 20 is end-of-life and gets no fix for CVE-2026-111497 (Brotli decoder
// out-of-bounds write); 22.23.4+ is patched.
const defaultNodeVersion = "22"

// NodeJSDetector detects Node.js applications
type NodeJSDetector struct{}

// Name returns the language name
func (d *NodeJSDetector) Name() string {
	return "Node.js"
}

// Detect checks if the project is a Node.js application
func (d *NodeJSDetector) Detect(files []string) (bool, string) {
	if !containsFile(files, "package.json") {
		return false, ""
	}

	version := defaultNodeVersion

	// Try to detect version from package.json engines field
	// In practice, we'd parse package.json here
	// For now, return default version
	return true, version
}

// GenerateDockerfile generates a Dockerfile for Node.js applications
func (d *NodeJSDetector) GenerateDockerfile(opts GenerateOptions) (string, error) {
	nodeVersion := opts.NodeVersion
	if nodeVersion == "" {
		nodeVersion = defaultNodeVersion
	}

	port := opts.Port
	if port == 0 {
		port = 3000
	}

	// Detect start command based on files
	startCommand := d.detectStartCommand(opts.Files)

	// Detect if it's a Next.js app
	isNextJS := d.isNextJS(opts.Files)
