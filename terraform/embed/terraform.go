package embed

import (
	"os"
	"path/filepath"
	"runtime"
)

// TerraformDir returns the absolute path to the terraform directory.
// This is useful for tests that need to run terraform commands against
// the actual configuration files.
func TerraformDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filename))
}

// ConsumerDir returns the absolute path to the dev consumer (terraform/gce/) directory.
func ConsumerDir() string {
	return filepath.Join(TerraformDir(), "gce")
}

// ModuleDir returns the absolute path to the containarium module directory.
func ModuleDir() string {
	return filepath.Join(TerraformDir(), "modules", "containarium")
}

// AllFiles returns all Terraform and script files by reading them from disk.
// This replaces the previous go:embed approach which couldn't reference parent directories.
func AllFiles() map[string]string {
	files := make(map[string]string)

	// Read module files
	moduleFiles := []string{
		"main.tf",
		"sentinel.tf",
		"spot-instance.tf",
		"variables.tf",
		"outputs.tf",
	}
	for _, f := range moduleFiles {
		content, err := os.ReadFile(filepath.Join(ModuleDir(), f))
		if err == nil {
			files[filepath.Join("modules", "containarium", f)] = string(content)
		}
	}

	// Read module scripts
	scriptFiles := []string{
		"startup.sh",
		"startup-spot.sh",
		"startup-sentinel.sh",
	}
	for _, f := range scriptFiles {
		content, err := os.ReadFile(filepath.Join(ModuleDir(), "scripts", f))
		if err == nil {
			files[filepath.Join("modules", "containarium", "scripts", f)] = string(content)
		}
	}

	// Read consumer files
	consumerFiles := []string{
		"main.tf",
		"variables.tf",
		"outputs.tf",
	}
	for _, f := range consumerFiles {
		content, err := os.ReadFile(filepath.Join(ConsumerDir(), f))
		if err == nil {
			files[filepath.Join("gce", f)] = string(content)
		}
	}

	return files
}

// TerraformFiles returns module Terraform files by reading from disk.
func TerraformFiles() map[string]string {
	files := make(map[string]string)
	moduleFiles := []string{
		"main.tf",
		"sentinel.tf",
		"spot-instance.tf",
		"variables.tf",
		"outputs.tf",
	}
	for _, f := range moduleFiles {
		content, err := os.ReadFile(filepath.Join(ModuleDir(), f))
		if err == nil {
			files[f] = string(content)
		}
	}
	return files
}

// ScriptFiles returns script files by reading from disk.
func ScriptFiles() map[string]string {
	files := make(map[string]string)
	scriptFiles := []string{
		"startup.sh",
		"startup-spot.sh",
		"startup-sentinel.sh",
	}
	for _, f := range scriptFiles {
		content, err := os.ReadFile(filepath.Join(ModuleDir(), "scripts", f))
		if err == nil {
			files[filepath.Join("scripts", f)] = string(content)
		}
	}
	return files
}
