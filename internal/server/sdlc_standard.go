package server

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/pkg/core/sdlcskills"
	"github.com/footprintai/containarium/pkg/core/skills"
)

// sdlcBundleEnv names the pinned sdlc-skills.tar.gz (the release asset the
// boxes install) for the daemon to compose hosted skill prompts from (#2488).
const sdlcBundleEnv = "CONTAINARIUM_SDLC_SKILLS_BUNDLE"

// loadSDLCBundle is the production loader: the bundle verified against the pin
