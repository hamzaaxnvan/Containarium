package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These are hermetic orchestration tests: they drive the whole runQuickstart
// flow — step order, flag wiring, the files it writes, the argv it would
// launch — with the daemon RPCs and the agent exec replaced by recording
// seams. No daemon, no Incus, no real agent; safe to run in CI as a normal
// unit test. The daemon/Incus- and installer-level integration tests live
// separately (see the PR description) because they need real infrastructure.

type qsRecord struct {
	createCalled bool
	createName   string
	createSSHKey string
	createStack  string
	syncCalled   bool
	exposeCalled bool
	exposeName   string
	exposePort   int
	exposeDomain string
	launchCalled bool
	launchAgent  string
	launchInstr  string
	// key wait (#2013)
	waitCalled       bool
	waitName         string
	waitBeforeLaunch bool
}

// installQuickstartHarness resets the quickstart flag globals to their init
// defaults, points HOME at a temp dir (no sudo), and swaps the four
// side-effecting seams for recorders. Everything is restored on cleanup so the
// rest of the cmd package's tests are unaffected.
func installQuickstartHarness(t *testing.T) (*qsRecord, string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")

	oKey, oStack, oCPU, oMem := qsSSHKeyPath, qsStack, qsCPU, qsMemory
	oSent, oPrompt, oDom, oAgent, oName := qsSentinel, qsPrompt, qsDomain, qsAgent, qsAgentName
	oExpose, oSkipMCP, oSkipInc, oNoLaunch := qsExposePort, qsSkipMCP, qsSkipInclude, qsNoLaunch
	oServer := serverAddr
	oCreate, oSync, oExposeFn, oLaunch := qsStepCreate, qsStepSSHConfig, qsStepExposePort, qsStepLaunchAgent
	oWait := qsStepWaitForKey
	t.Cleanup(func() {
		qsSSHKeyPath, qsStack, qsCPU, qsMemory = oKey, oStack, oCPU, oMem
		qsSentinel, qsPrompt, qsDomain, qsAgent, qsAgentName = oSent, oPrompt, oDom, oAgent, oName
		qsExposePort, qsSkipMCP, qsSkipInclude, qsNoLaunch = oExpose, oSkipMCP, oSkipInc, oNoLaunch
		serverAddr = oServer
		qsStepCreate, qsStepSSHConfig, qsStepExposePort, qsStepLaunchAgent = oCreate, oSync, oExposeFn, oLaunch
		qsStepWaitForKey = oWait
	})

	// Defaults mirror init().
	qsSSHKeyPath, qsStack, qsCPU, qsMemory = "", "fullstack", "4", "4GB"
	qsSentinel, qsPrompt, qsDomain, qsAgent, qsAgentName = "", "", "", "claude", "containarium-box"
	qsExposePort, qsSkipMCP, qsSkipInclude, qsNoLaunch = 8080, false, false, false
	serverAddr = ""

