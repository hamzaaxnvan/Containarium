package container

import "testing"

// TestCollaboratorJumpAccountContainer pins the #1140 mapping: a collaborator
// jump account "<owner>-container-<collab>" resolves to the owner container
// "<owner>-container", while a bare owner account (or any non-collaborator
// name) reports false so the caller falls back to "<name>-container".
func TestCollaboratorJumpAccountContainer(t *testing.T) {
	tests := []struct {
		name   string
