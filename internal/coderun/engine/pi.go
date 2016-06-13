package engine

import (
	"strings"
)

// piEngine is pi (https://pi.dev, @earendil-works/pi-coding-agent) — a
// local-first coding agent that runs INSIDE the box rather than driving it from
// a laptop over MCP. docs/integrations/pi.md argues why that is the right shape
// for a Containarium box: pi has no MCP client by design, so its native
// read/write/edit/bash tools act on the box's filesystem directly and agent-box
// is redundant on this path.
//
// pi is the reason #1727 exists: it accepts a custom OpenAI-compatible provider
// in ~/.pi/agent/models.json whose apiKey is read from the environment, which is
// exactly the shape a scoped gateway token fits. A box running pi through the
// gateway holds no provider key anywhere.
type piEngine struct {
	opts Options
}

// PiVersion is the pinned pi version `code install --engine pi` installs.
//
// Pinned, not floating, for a concrete reason: models.json is a third-party
// schema we do not own, and TestPiModelsJSON pins our rendered output against a
// fixture taken from THIS version's own docs. A floating install would let pi
// rename a field and turn that fixture into a lie on the next box someone
// provisions. Bump this and the fixture together, or not at all.
const PiVersion = "0.87.1"

// PiPackage is pi's npm package. docs/integrations/pi.md names it, and it is
// also what pi.dev/install.sh itself installs.
const PiPackage = "@earendil-works/pi-coding-agent"

// PiNodeMinVersion is pi's own engines.node floor (package.json of
// @earendil-works/pi-coding-agent@0.87.1: ">=22.19.0"). Checked explicitly so a
// box without new-enough Node fails naming the fix, instead of failing inside
// npm's EBADENGINE output.
const PiNodeMinVersion = "22.19.0"

