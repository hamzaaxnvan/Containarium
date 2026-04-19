//go:build !containarium_client

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Upgrade-and-rollback runbook gate (#2563). An operator follows the runbook
// under pressure, mid-incident; a renamed verb, a dropped flag or a dead link
// there costs the most at the worst moment. These tests hold the runbook and
// the drill-record template to the real cobra tree (`containarium` and
// `containariumd` share one tree, see readme_drift_test.go), check every
// in-page and relative link resolves, and check the supported-release policy
// block is identical in SECURITY.md and the runbook.

var runbookDocs = []string{
	"../../docs/UPGRADE-AND-ROLLBACK.md",
	"../../docs/DRILL-RECORD-TEMPLATE.md",
}

const (
	runbookPath        = "../../docs/UPGRADE-AND-ROLLBACK.md"
	securityPolicyPath = "../../SECURITY.md"
	policyBegin        = "<!-- support-policy:begin -->"
	policyEnd          = "<!-- support-policy:end -->"
)

var runbookBinaries = map[string]bool{"containarium": true, "containariumd": true}

var (
	// inlineCLIRE matches an inline code span that starts with a binary name.
	inlineCLIRE = regexp.MustCompile("`((?:containariumd|containarium) [^`]+)`")
	// relLinkRE matches an inline Markdown link target.
	relLinkRE = regexp.MustCompile(`\]\(([^)\s]+)\)`)
)

// inlineInvocations returns the `containarium …` commands written as inline
// code spans in prose, outside fences. Steps cite verbs that way as often as
// they put them in a shell block, so both must resolve.
func inlineInvocations(markdown string) []cliInvocation {
	var out []cliInvocation
	inFence := false
	for i, line := range strings.Split(markdown, "\n") {
		if isFence, _ := fenceInfo(line); isFence {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range inlineCLIRE.FindAllStringSubmatch(line, -1) {
			for _, words := range splitShell(m[1]) {
				if args, ok := containariumArgs(words, runbookBinaries); ok {
					out = append(out, cliInvocation{Line: i + 1, Args: args})
				}
			}
		}
	}
	return out
}

// checkRelativeLinks reports every relative link outside fences whose file
// does not exist, and every `other.md#anchor` whose anchor is not a heading
// in that file. Pure in-page links (#x) are checkAnchors' job.
func checkRelativeLinks(docPath, markdown string) []driftProblem {
	var out []driftProblem
	inFence := false
	for i, line := range strings.Split(markdown, "\n") {
		if isFence, _ := fenceInfo(line); isFence {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range relLinkRE.FindAllStringSubmatch(line, -1) {
			target := m[1]
			if strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			path, frag, _ := strings.Cut(target, "#")
			full := filepath.Join(filepath.Dir(docPath), path)
			if _, err := os.Stat(full); err != nil {
				out = append(out, driftProblem{i + 1, fmt.Sprintf("link to %s: file not found", path)})
				continue
			}
			if frag == "" || !strings.HasSuffix(path, ".md") {
				continue
			}
			b, err := os.ReadFile(full) // #nosec G304 -- test reads repo docs
			if err != nil {
				out = append(out, driftProblem{i + 1, fmt.Sprintf("link to %s: %v", path, err)})
				continue
			}
			if !anchorTargets(string(b))[strings.ToLower(frag)] {
				out = append(out, driftProblem{i + 1, fmt.Sprintf("link to %s#%s has no matching heading", path, frag)})
			}
		}
	}
	return out
}

// checkRunbook runs every runbook check over one document.
func checkRunbook(root *cobra.Command, docPath, markdown string) []driftProblem {
	var out []driftProblem
	invs := append(extractInvocationsFor(markdown, runbookBinaries), inlineInvocations(markdown)...)
	for _, inv := range invs {
		out = append(out, checkInvocation(root, inv)...)
	}
	out = append(out, checkAnchors(markdown)...)
	return append(out, checkRelativeLinks(docPath, markdown)...)
}

func TestUpgradeRunbook_CommandsAndLinksResolve(t *testing.T) {
	for _, doc := range runbookDocs {
		md := readDriftFile(t, doc)
		for _, p := range checkRunbook(rootCmd, doc, md) {
			t.Errorf("%s:%d: %s", filepath.Base(doc), p.Line, p.Detail)
		}
