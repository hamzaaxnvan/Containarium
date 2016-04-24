package runlease

import (
	"sync"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Info is what other daemon components need to know about a
// currently-running skill run, keyed by run id — enough to resolve a
// verified JWT's run_id claim to the skill/model it belongs to, e.g.
// for the tracker broker's platform-stamped identity (#1922), and
// (#1923) enough to resolve it to the box/workspace/base-commit
// SubmitTrackerChange needs to build a bundle without a second lookup
// path or its own copy of what provisionSkillBox already knows.
type Info struct {
	SkillID string
	Model   string
	// Engine is the engine this run actually resolved to (#2222) —
	// AGENT_ENGINE_UNSPECIFIED for a run that never named one and got no
	// gateway default either (direct mode, no engine). Recorded beside Model
	// so a reader resolving a run_id learns both in one lookup, the same way
	// it already learns Model.
	Engine pb.AgentEngine
	// Box is the container name the run's box lives in — the same
