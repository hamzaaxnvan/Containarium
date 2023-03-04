package client

import "testing"

// The gRPC create request carries --labels (#2358).
func TestNewCreateContainerRequest_Labels(t *testing.T) {
	labels := map[string]string{"cloud_org_id": "org-a"}
	req := NewCreateContainerRequest("alice", "img", "1", "1GB", "10GB", nil, false, "", nil, 0, 0, false, "", "", GitSourceOpts{}, 0, 0, false, 0, "", EncryptionOpts{}, "", "", "", labels)
	if req.Username != "alice" || req.Labels["cloud_org_id"] != "org-a" {
