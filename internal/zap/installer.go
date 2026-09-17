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

	log.Printf("Installer: downloading ZAP %s into security container", tag)

	// Install Java (ZAP requirement) and download+extract ZAP inside the container
	script := fmt.Sprintf(`set -e
# Install Java if not present
if ! command -v java >/dev/null 2>&1; then
    apt-get update -qq && apt-get install -y -qq default-jre-headless wget >/dev/null 2>&1
fi
# Create install dir
mkdir -p %s
# Download and extract
cd /tmp
wget -q -O zap.tar.gz "%s"
tar xzf zap.tar.gz -C %s
rm -f zap.tar.gz
# Create stable symlink
ln -sfn %s/ZAP_%s %s/ZAP
chmod +x %s/ZAP/zap.sh
echo "ZAP %s installed successfully"
`, zapInstallDir, downloadURL, zapInstallDir, zapInstallDir, version, zapInstallDir, zapInstallDir, version)

	cmd := exec.Command("incus", "exec", SecurityContainerName, "--", "bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to install ZAP in security container: %w (%s)", err, string(out))
	}

	log.Printf("Installer: %s", strings.TrimSpace(string(out)))
	return nil
}

// githubRelease is a subset of the GitHub releases API response.
type githubRelease struct {
	TagName string `json:"tag_name"`
}

// resolveLatestZapVersion fetches the latest stable release tag from ZAP's GitHub repo
func resolveLatestZapVersion() (string, error) {
	resp, err := http.Get("https://api.github.com/repos/zaproxy/zaproxy/releases/latest")
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest ZAP release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to decode release: %w", err)
	}
	if release.TagName == "" {
		return "", fmt.Errorf("no tag found in latest ZAP release")
	}
	return release.TagName, nil
}
