package server

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

const (
	// envGrafanaAdminPasswordFile overrides where the generated Grafana admin
	// password is kept on the host.
	envGrafanaAdminPasswordFile = "CONTAINARIUM_GRAFANA_ADMIN_PASSWORD_FILE" // #nosec G101 -- env var name, not a credential value

	// defaultGrafanaAdminPasswordFile sits next to the Postgres password file:
	// root-only, readable by the operator who administers the host.
	defaultGrafanaAdminPasswordFile = "/etc/containarium/grafana-admin.password" // #nosec G101 -- a file PATH, not a credential

	// grafanaAdminPasswordLen is long enough that guessing is not the threat,
	// and short enough to type if an operator has to.
	grafanaAdminPasswordLen = 32

	secretAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// generateSecret returns n random alphanumeric characters from crypto/rand.
