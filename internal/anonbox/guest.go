package anonbox

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/box"
)

// guestFiles is what the banner and claim-url need to say.
type guestFiles struct {
	expiresAt       time.Time
	limits          Limits
	previousExpired bool
	claimURL        string // empty = no claim-url file
}

// writeGuestFiles drops the login banner (and the claim URL when a minter
// is configured) into the guest. Parent directories are created first:
// the Incus file API does not create them.
func (m *Manager) writeGuestFiles(ctx context.Context, ref box.BoxRef, g guestFiles) error {
	dirs := []string{path.Dir(BannerPath), path.Dir(RemindFilePath)}
	if g.claimURL != "" && path.Dir(ClaimURLPath) != path.Dir(RemindFilePath) {
		dirs = append(dirs, path.Dir(ClaimURLPath))
	}
	if _, stderr, err := m.boxes.Exec(ctx, ref, append([]string{"mkdir", "-p"}, dirs...)); err != nil {
		return fmt.Errorf("mkdir %v: %w (%s)", dirs, err, strings.TrimSpace(stderr))
	}
	if err := m.boxes.WriteFile(ctx, ref, BannerPath, []byte(banner(g)), "0755"); err != nil {
		return fmt.Errorf("write banner: %w", err)
	}
	if g.claimURL != "" {
		if err := m.boxes.WriteFile(ctx, ref, ClaimURLPath, []byte(g.claimURL+"\n"), "0644"); err != nil {
