package container

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// CreateBackupVerifier creates a fresh, daemon-owned Postgres container for
// one verification. It does not create a tenant login or copy source data or
// configuration. The caller must authorize tenant before calling and always
// invoke cleanup. A unique name keeps concurrent verifications isolated.
func (m *Manager) CreateBackupVerifier(tenant string) (name, password string, cleanup func() error, err error) {
