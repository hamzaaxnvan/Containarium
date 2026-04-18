package autosleep

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// readSource returns the file's contents — used by tests that pin a
// source-level contract that can't be observed at runtime without
// real infrastructure (e.g. the audit_logs row written by the adapter).
func readSource(relPath string) (string, error) {
	b, err := os.ReadFile(relPath)
	if err != nil {
		return "", err
	}
	return string(b), nil
