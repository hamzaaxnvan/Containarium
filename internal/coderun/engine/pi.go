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

// piAgentDir is pi's user-level config directory (pi's own
// docs/configuration.md: "the agent directory, which defaults to ~/.pi/agent").
// Written absolute-from-$HOME rather than ~-relative because these strings are
// tested by the shell, not expanded by it.
const piAgentDir = "$HOME/.pi/agent"

// piModelsPath is the custom-provider config `code install` renders.
const piModelsPath = piAgentDir + "/models.json"

// piGatewayEnvPath is the 0600 file `code run` writes the per-run gateway token
// to before process_start. Contract C4 puts it at ~/.pi/gateway.env — beside
// pi's state, not inside agent/, so pi never tries to parse it as config.
const piGatewayEnvPath = "$HOME/.pi/gateway.env"

// piBin is where the install lands pi. $HOME/.local/bin, matching
// ~/.local/bin/claude and ~/.local/bin/agent-box, so the whole command stays
// user-level with no sudo anywhere.
const piBin = "$HOME/.local/bin/pi"

func (piEngine) Name() Name                     { return NamePi }
func (e piEngine) Credential() CredentialSource { return e.opts.Credential }
func (piEngine) GatewayEnvPath() string         { return piGatewayEnvPath }

// InstallScript installs pi at a pinned version and writes its
// custom-provider config.
//
// It does NOT use pi's own install.sh, and that is a deliberate deviation from
// the issue's wording. The real https://pi.dev/install.sh (a) takes no version
// argument — the version comes from release metadata it fetches, so there is
// nothing to pin — and (b) opens /dev/tty for a logo animation, `stty`, and an
// interactive "install Node for you?" prompt. `code install` execs over ssh with
// no TTY, so that installer is a hang, not a pin. The npm line below is the
// alternative docs/integrations/pi.md already documents, and it takes a version.
//
// npm's prefix is pointed at $HOME/.local so `npm i -g` needs no root, and
// --ignore-scripts is pi.md's own recommendation.
func (piEngine) InstallScript(o InstallOptions) string {
	version := strings.TrimSpace(o.Version)
	if version == "" {
		version = PiVersion
	}
