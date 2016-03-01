package buildpack

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Language represents a detected programming language
type Language struct {
	Name    string
	Version string
}

// GenerateOptions contains options for generating a Dockerfile
type GenerateOptions struct {
	Port          int
	Files         []string
	NodeVersion   string
	PythonVersion string
	GoVersion     string
	RustVersion   string
}

// LanguageDetector detects languages and generates Dockerfiles
type LanguageDetector interface {
	Name() string
	Detect(files []string) (bool, string)
	GenerateDockerfile(opts GenerateOptions) (string, error)
