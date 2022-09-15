package server

import (
	"context"
	"sync"
	"testing"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// countingPolicyConfig is an in-memory PolicyConfigStore that counts reads.
type countingPolicyConfig struct {
	mu   sync.Mutex
	kv   map[string]string
	gets int
}

func (c *countingPolicyConfig) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets++
	v, ok := c.kv[key]
	if !ok {
		return "", app.ErrDaemonConfigNotFound
	}
	return v, nil
}

func (c *countingPolicyConfig) Set(_ context.Context, key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kv[key] = value
	return nil
}

