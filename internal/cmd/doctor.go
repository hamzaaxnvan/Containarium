//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/internal/hostcheck"
	"github.com/footprintai/containarium/internal/policyreadiness"
)

// doctor — the host capability self-check from the daemon deploy contract
// (prd/oss/daemon-deploy-contract.md). It catches the "capability trap": a
// systemd unit that looks fine (User=root) but whose NoNewPrivileges /
// missing AmbientCapabilities / missing ReadWritePaths silently break
// `useradd` — so the daemon starts "successfully" and only fails on the
// FIRST container create, hours later. The definitive check is a live
// useradd/userdel probe.
//
// The check logic lives in internal/hostcheck so the daemon's cloud status
// probe can run the same checks (#528); this file is the CLI shell.
//
// Run it under the SAME constraints as the daemon (i.e. via the systemd
// unit, or as the daemon) for an accurate result — a plain root shell has
// full caps and will pass even if the daemon unit is mis-configured.

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run the host capability self-check (the deploy-contract preflight)",
	Long: `Verify this host can actually run the Containarium daemon's user-management
operations — the checks behind the "capability trap": running uid/caps,
writability of the daemon's paths, and a LIVE useradd/userdel probe.

Run under the same constraints as the daemon (via its systemd unit) for an
accurate result; a plain root shell has full capabilities and will pass even
when the daemon unit is mis-configured. Exits non-zero if any required check
fails.`,
	RunE: runDoctor,
}

// doctorDaemonURL is the local daemon's REST gateway, read for policy-store
// readiness (#2483).
var doctorDaemonURL string

func init() {
	doctorCmd.Flags().StringVar(&doctorDaemonURL, "daemon-url", "http://localhost:8080",
		"local daemon REST gateway to read policy-store readiness from (advisory)")
	rootCmd.AddCommand(doctorCmd)
}

// hostDoctorChecks runs every host capability check. Thin alias over
// hostcheck.Run so existing callers (`pool join`) stay unchanged.
func hostDoctorChecks() []hostcheck.Check { return hostcheck.Run() }

func runDoctor(cmd *cobra.Command, args []string) error {
	checks := hostDoctorChecks()
	failed := printDoctor(checks)

	// Host security posture (#1103), printed as its own section and NOT
	// folded into `failed`. The separation is the point: for BYOC the
	// machine is the customer's, so "can run workloads" and "is safe to
	// run them on" are different questions and a functional pass must not
	// read as a security pass. Whether a posture miss should block is an
	// open product decision, so nothing here changes the exit code.
	printPosture(hostcheck.RunPosture())

	// Policy-store readiness from the running daemon (#2483), advisory.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 3 * time.Second}
	printDaemonReadiness(os.Stdout, policyreadiness.Run(ctx, policyreadiness.HTTPCheck(client, doctorDaemonURL)))

	if failed > 0 {
		return fmt.Errorf("doctor: %d required check(s) failed — this host cannot run the daemon's user management until fixed (see prd daemon-deploy-contract)", failed)
	}
	return nil
}

// printPosture renders the posture group. Returns nothing on purpose —
// there is no count to act on, because no posture result gates anything.
func printPosture(checks []hostcheck.Check) {
	if len(checks) == 0 {
		return
	}
	unmet := 0
	fmt.Println()
	fmt.Println("Host security posture (advisory — does not affect the exit code):")
	for _, c := range checks {
		mark := "✓"
		if !c.OK {
			mark = "!"
			unmet++
		}
		line := fmt.Sprintf("  %s %s", mark, c.Name)
		if c.Detail != "" {
			line += " — " + c.Detail
		}
		fmt.Println(line)
	}
	if unmet > 0 {
		fmt.Printf("\n  %d posture item(s) unmet or unverifiable. A check that could not gather\n"+
