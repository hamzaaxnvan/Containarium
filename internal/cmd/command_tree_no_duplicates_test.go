package cmd

import "testing"

// TestNoDuplicateTopLevelCommandNames guards the class of bug found while
// implementing #1921: "collaborator" was registered on rootCmd twice under
