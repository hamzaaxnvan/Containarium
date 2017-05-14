package alert

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Manager handles syncing alert rules from the database to vmalert inside
// the VictoriaMetrics container.
type Manager struct {
	store         *Store
	incusClient   *incus.Client
	containerName string
}

// NewManager creates a new alert rules manager
func NewManager(store *Store, incusClient *incus.Client, containerName string) *Manager {
	return &Manager{
		store:         store,
		incusClient:   incusClient,
		containerName: containerName,
	}
}

// SyncRules reads all enabled rules from the database, generates a YAML file,
// writes it to the container, and reloads vmalert.
func (m *Manager) SyncRules(ctx context.Context) error {
	rules, err := m.store.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("failed to list enabled rules: %w", err)
	}

	yaml := generateRulesYAML(rules)

	// Write custom rules file
	if err := m.incusClient.WriteFile(
		m.containerName,
		"/etc/vmalert/rules/custom.yml",
		[]byte(yaml),
		"0644",
	); err != nil {
		return fmt.Errorf("failed to write custom rules: %w", err)
	}

	// Reload vmalert
	if err := m.incusClient.Exec(m.containerName, []string{
		"curl", "-sf", "-X", "POST", "http://localhost:8880/-/reload",
	}); err != nil {
		log.Printf("Warning: vmalert reload returned error (may still succeed): %v", err)
	}

	log.Printf("Synced %d custom alert rules to vmalert", len(rules))
	return nil
}

