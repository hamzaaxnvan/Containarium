package sentinel

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
)

// GCPProvider implements CloudProvider and EventWatcher using the GCP Compute Engine API.
type GCPProvider struct {
	client    *compute.InstancesClient
	opsClient *compute.ZoneOperationsClient
	project   string
	zone      string
	instance  string
}

// NewGCPProvider creates a GCP cloud provider.
// Auth is handled via the metadata server (on GCE) or GOOGLE_APPLICATION_CREDENTIALS.
func NewGCPProvider(ctx context.Context, project, zone, instanceName string) (*GCPProvider, error) {
	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP compute client: %w", err)
	}
	opsClient, err := compute.NewZoneOperationsRESTClient(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to create GCP operations client: %w", err)
	}
	return &GCPProvider{
		client:    client,
		opsClient: opsClient,
		project:   project,
		zone:      zone,
		instance:  instanceName,
	}, nil
}

func (g *GCPProvider) GetInstanceStatus(ctx context.Context) (InstanceStatus, error) {
