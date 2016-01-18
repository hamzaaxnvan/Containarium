package container

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetUserShell_Nologin(t *testing.T) {
	// When containarium-shell doesn't exist, should return nologin
	// Save and restore the constant
	origPath := containerShellPath

	// Point to a non-existent path
	// Note: containerShellPath is a const, so we test the function logic directly
