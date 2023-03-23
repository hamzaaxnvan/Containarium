package zap

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
)

const (
	// SecurityContainerName is the Incus container where ZAP is installed
	SecurityContainerName = "containarium-core-security"
	// zapInstallDir is the install directory inside the security container
	zapInstallDir = "/opt/zap"
)

// Installer handles downloading and installing OWASP ZAP inside the security container.
type Installer struct {
	mu sync.Mutex
}

// NewInstaller creates a new Installer.
func NewInstaller() *Installer {
	return &Installer{}
}

// IsInstalled checks if ZAP is installed in the security container
func (i *Installer) IsInstalled() bool {
	cmd := exec.Command("incus", "exec", SecurityContainerName, "--",
		"test", "-x", zapInstallDir+"/ZAP/zap.sh")
	return cmd.Run() == nil
}

// InstallZap downloads and installs the latest OWASP ZAP inside the security container.
func (i *Installer) InstallZap() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.IsInstalled() {
		return fmt.Errorf("ZAP is already installed")
	}

	tag, err := resolveLatestZapVersion()
	if err != nil {
		return fmt.Errorf("failed to resolve ZAP version: %w", err)
	}

	version := strings.TrimPrefix(tag, "v")

	// ZAP stable release format: ZAP_<version>_Linux.tar.gz
	filename := fmt.Sprintf("ZAP_%s_Linux.tar.gz", version)
	downloadURL := fmt.Sprintf("https://github.com/zaproxy/zaproxy/releases/download/%s/%s", tag, filename)

