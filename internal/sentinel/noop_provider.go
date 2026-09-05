package sentinel

import "context"

// NoOpProvider is a cloud provider for local testing.
// It always reports the backend as running and does nothing on start.
type NoOpProvider struct {
	backendIP string
}

// NewNoOpProvider creates a provider that returns a fixed backend IP
// and performs no cloud operations. Used with --provider=none.
func NewNoOpProvider(backendIP string) *NoOpProvider {
	return &NoOpProvider{backendIP: backendIP}
}

func (n *NoOpProvider) GetInstanceStatus(ctx context.Context) (InstanceStatus, error) {
	return StatusRunning, nil
}

func (n *NoOpProvider) GetInstanceIP(ctx context.Context) (string, error) {
	return n.backendIP, nil
}

func (n *NoOpProvider) StartInstance(ctx context.Context) error {
	return nil
}
