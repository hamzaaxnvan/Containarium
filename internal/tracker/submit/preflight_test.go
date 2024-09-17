package submit

import (
	"errors"
	"testing"
)

func TestCheckHostGit_RealGitOnPath(t *testing.T) {
	version, err := CheckHostGit()
	if err != nil {
		t.Fatalf("CheckHostGit: %v (this environment's git must be >= %d.%d for the submit path to work at all)", err, MinGitMajor, MinGitMinor)
	}
