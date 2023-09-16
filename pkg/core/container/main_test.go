package container

import (
	"os"
	"testing"
)

// TestMain keeps the keyless create path (#2489) from running host useradd in
// unit tests: every test that creates a box without SSH keys would otherwise
