package mcp

import (
	"context"
	"testing"
)

// #1216: `runner provision` / `provision_runners` could not say WHERE the
// runner boxes should land, while the create primitive underneath them
// could. So the lower-level call expressed placement and the workflow built
// on top of it could not — runners always landed on the default backend.
//
// The acceptance criterion this covers is deliberately about the wire, not
// the flag: "a test asserts the placement argument reaches the create call
// rather than being silently dropped". A flag that parses and then goes
// nowhere is the failure mode, and it looks identical to a working one.

// capturingCreateAPI records the CreateContainerRequest the runner creator
// builds.
type capturingCreateAPI struct {
	API
	got CreateContainerRequest
}

func (c *capturingCreateAPI) CreateContainer(req CreateContainerRequest) (*CreateContainerResponse, error) {
	c.got = req
	return &CreateContainerResponse{
		Container: Container{Name: req.Username, Username: req.Username},
	}, nil
}

// buildCreator extracts the creator the runner deps are wired with, by
// building the deps and invoking the box manager's create path.
func createWithPlacement(t *testing.T, place placement) CreateContainerRequest {
	t.Helper()
	api := &capturingCreateAPI{}

	// withSSH=false skips the sentinel/SSH-key requirements; the creator is
	// wired identically either way, and the creator is what is under test.
	deps, _, err := buildMCPRunnerDeps(api, "", "", false, place)
	if err != nil {
		t.Fatalf("buildMCPRunnerDeps: %v", err)
	}
	if _, _, err := deps.Boxes.Create(context.Background(), "ci-runner-1", "ssh-ed25519 AAAA test"); err != nil {
		t.Fatalf("create: %v", err)
	}
