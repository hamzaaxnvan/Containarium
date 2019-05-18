package hosting

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DefaultConfigDir is the default directory for hosting configuration
	DefaultConfigDir = "/etc/containarium"

	// DefaultConfigFile is the default hosting configuration file
	DefaultConfigFile = "hosting.json"
)

// Config holds the hosting configuration including provider credentials
type Config struct {
	// Provider is the DNS provider name (e.g., "godaddy")
	Provider string `json:"provider"`

