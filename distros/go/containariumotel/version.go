package containariumotel

import (
	_ "embed"
	"strings"
)

// versionEmbed is the raw contents of the VERSION file, baked in at
// build time. We use //go:embed (Phase 6 of the rollout plan) instead
// of a hand-edited const so a single file bump at release time
// updates both the package's source-of-truth value and the
// containarium.distro resource attribute stamp.
