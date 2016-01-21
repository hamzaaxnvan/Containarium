package buildpack

import (
	"fmt"
)

// StaticDetector detects static HTML/CSS/JS applications
type StaticDetector struct{}

// Name returns the language name
func (d *StaticDetector) Name() string {
	return "Static"
}

// Detect checks if the project is a static web application
func (d *StaticDetector) Detect(files []string) (bool, string) {
	// Check for index.html
	if containsFile(files, "index.html") {
		return true, ""
	}

	// Check for common static site generator outputs
	if containsFile(files, "public/index.html") || containsFile(files, "dist/index.html") {
		return true, ""
	}

	return false, ""
}

// GenerateDockerfile generates a Dockerfile for static web applications
func (d *StaticDetector) GenerateDockerfile(opts GenerateOptions) (string, error) {
	port := opts.Port
	if port == 0 {
		port = 80
	}

	// Detect the document root
	docRoot := d.detectDocRoot(opts.Files)

	return fmt.Sprintf(`# Auto-generated Dockerfile for Static Site
FROM nginx:alpine

# Copy static files
COPY %s /usr/share/nginx/html

# Configure nginx for SPA routing (if needed)
