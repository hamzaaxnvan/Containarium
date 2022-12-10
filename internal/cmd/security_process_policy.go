// `containarium security process-policy get|set|release` — what a HIGH
// mining finding does to its container (#2636): ALERT, THROTTLE or FREEZE,
// with a one-command release. CLI-first per the repo convention; talks to the
// grpc-gateway REST routes with the generated protobuf types (protojson), so
// the action enum is parsed to ProcessAction and never sent as free text.
package cmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const processPolicyPath = "/v1/security/process-policy"

var securityProcessPolicyCmd = &cobra.Command{
	Use:   "process-policy",
	Short: "What a HIGH mining finding does to its container: alert, throttle or freeze (#2636)",
}

// processActionNames maps the CLI spelling to the proto enum.
var processActionNames = map[string]pb.ProcessAction{
	"alert":    pb.ProcessAction_PROCESS_ACTION_ALERT,
	"throttle": pb.ProcessAction_PROCESS_ACTION_THROTTLE,
	"freeze":   pb.ProcessAction_PROCESS_ACTION_FREEZE,
}

const processActionAllowed = "alert, throttle, freeze"

func parseProcessAction(s string) (pb.ProcessAction, error) {
	a, ok := processActionNames[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return pb.ProcessAction_PROCESS_ACTION_UNSPECIFIED,
			fmt.Errorf("invalid --action %q: allowed values are %s", s, processActionAllowed)
	}
	return a, nil
}

var (
	allowancePercentRe = regexp.MustCompile(`^[0-9]+%$`)
	allowanceQuotaRe   = regexp.MustCompile(`^[0-9]+ms/[0-9]+ms$`)
)

func parsePositiveDuration(flag, s string) (*durationpb.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return nil, fmt.Errorf("invalid --%s %q: want a positive duration such as 24h or 5m", flag, s)
	}
	return durationpb.New(d), nil
}

func parseStratumPorts(s string) ([]uint32, error) {
	var out []uint32
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.ParseUint(strings.TrimSpace(f), 10, 32)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid --stratum-ports entry %q: want ports 1-65535, comma separated", f)
		}
		out = append(out, uint32(n))
	}
	return out, nil
}

// policyRoundTrip does an admin-authenticated request and decodes the
// protojson response into out.
func policyRoundTrip(method, rawURL string, body proto.Message, out proto.Message) error {
	var rd io.Reader
	if body != nil {
		b, err := protojson.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, rawURL, rd)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 512 {
			msg = msg[:512]
		}
		return fmt.Errorf("status %d: %s", resp.StatusCode, msg)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func actionLabel(a pb.ProcessAction) string {
	return strings.TrimPrefix(a.String(), "PROCESS_ACTION_")
}

func writePolicyBlock(w io.Writer, p *pb.ProcessPolicy) {
	fmt.Fprintf(w, "  action:             %s\n", actionLabel(p.GetAction()))
	fmt.Fprintf(w, "  throttle allowance: %s\n", p.GetThrottleAllowance())
	fmt.Fprintf(w, "  ttl:                %s\n", p.GetTtl().AsDuration())
	kill := "inherit"
	if p != nil && p.KillProcess != nil {
		kill = fmt.Sprint(p.GetKillProcess())
	}
	fmt.Fprintf(w, "  kill process:       %s\n", kill)
}

func writeMiningThresholds(w io.Writer, m *pb.MiningBehaviorThresholds) {
	ports := make([]string, 0, len(m.GetStratumPorts()))
	for _, p := range m.GetStratumPorts() {
		ports = append(ports, strconv.FormatUint(uint64(p), 10))
	}
	fmt.Fprintln(w, "Mining-behavior thresholds:")
	fmt.Fprintf(w, "  min cores:              %g\n", m.GetMinCores())
	fmt.Fprintf(w, "  min fraction of limit:  %g\n", m.GetMinFractionOfLimit())
	fmt.Fprintf(w, "  window:                 %s\n", m.GetWindow().AsDuration())
	fmt.Fprintf(w, "  flow min age:           %s\n", m.GetFlowMinAge().AsDuration())
	fmt.Fprintf(w, "  stratum ports:          %s\n", strings.Join(ports, ","))
}

func renderProcessPolicy(w io.Writer, container string, r *pb.GetProcessPolicyResponse) {
	if container == "" {
		fmt.Fprintln(w, "Backend default")
	} else {
		fmt.Fprintf(w, "Container: %s\n", container)
	}
	switch {
	case r.GetPolicy() == nil && container == "":
		fmt.Fprintln(w, "Stored:    (none; the built-in default applies)")
	case r.GetPolicy() == nil:
		fmt.Fprintln(w, "Stored:    (none; inherits the backend default)")
	case r.GetIsOverride():
		fmt.Fprintf(w, "Stored:    %s (override)\n", actionLabel(r.GetPolicy().GetAction()))
	default:
		fmt.Fprintf(w, "Stored:    %s\n", actionLabel(r.GetPolicy().GetAction()))
	}
	fmt.Fprintln(w, "Effective:")
	writePolicyBlock(w, r.GetEffective())
	// The effective thresholds are the values the rule is using (stored
	// fields over built-in defaults). The stored message carries zeros for
	// every field that was never set, which is not what the rule uses.
	m := r.GetEffective().GetMiningBehavior()
	if m == nil {
		m = r.GetPolicy().GetMiningBehavior()
	}
	if m != nil {
		writeMiningThresholds(w, m)
	}
}

func newProcessPolicyGetCmd() *cobra.Command {
	var container string
	var jsonOut bool
	c := &cobra.Command{
		Use:   "get",
		Short: "Show the stored process policy and the effective policy the responder applies",
		Long: `Without --container, shows the backend default. With --container, shows that
container's override (if any). The effective policy resolves
override -> backend default -> built-in {ALERT, 10%, 24h, kill process off}.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if serverAddr == "" {
				return errServerRequired()
			}
			u := strings.TrimSuffix(serverAddr, "/") + processPolicyPath
			if container != "" {
				u += "?" + url.Values{"container": {container}}.Encode()
			}
			var out pb.GetProcessPolicyResponse
			if err := policyRoundTrip(http.MethodGet, u, nil, &out); err != nil {
				return err
			}
			if jsonOut {
				b, err := protojson.MarshalOptions{Multiline: true}.Marshal(&out)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return err
			}
			renderProcessPolicy(cmd.OutOrStdout(), container, &out)
			return nil
		},
	}
	c.Flags().StringVar(&container, "container", "", "Read this container's override instead of the backend default")
	c.Flags().BoolVar(&jsonOut, "json", false, "Output raw JSON")
	return c
}

func newProcessPolicySetCmd() *cobra.Command {
	var (
		action, allowance, ttl, container, rule string
		clearOverride, killProcess              bool
		minCores, minFraction                   float64
		window, flowMinAge, ports               string
	)
	c := &cobra.Command{
		Use:   "set",
		Short: "Set the backend default or a container's process policy (admin)",
		Long: `Sets what a HIGH mining finding does to its container.

  containarium security process-policy set --action throttle --throttle-allowance 10% --ttl 24h
  containarium security process-policy set --action freeze --container box-1
  containarium security process-policy set --container box-1 --clear-override
  containarium security process-policy set --kill-process=true
  containarium security process-policy set --rule mining-behavior --min-cores 2 --stratum-ports 3333,4444

--action is one of alert, throttle, freeze. --kill-process=true also SIGKILLs
the process of a miner-signature match from the host (default false; the
action still applies). Omitted fields inherit. The
mining-behavior thresholds are backend-wide and cannot be combined with
--container.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if serverAddr == "" {
				return errServerRequired()
			}
			flags := cmd.Flags()
			req := &pb.SetProcessPolicyRequest{Container: container}
			if clearOverride {
				if container == "" {
					return fmt.Errorf("--clear-override requires --container (only overrides can be cleared)")
				}
				for _, n := range []string{"action", "throttle-allowance", "ttl", "kill-process", "rule"} {
					if flags.Changed(n) {
						return fmt.Errorf("--clear-override cannot be combined with policy flags")
					}
				}
				req.ClearOverride = true
			} else {
				pol := &pb.ProcessPolicy{}
				set := false
				if flags.Changed("action") {
					a, err := parseProcessAction(action)
					if err != nil {
						return err
					}
					pol.Action, set = a, true
				}
				if flags.Changed("throttle-allowance") {
					if !allowancePercentRe.MatchString(allowance) && !allowanceQuotaRe.MatchString(allowance) {
						return fmt.Errorf("invalid --throttle-allowance %q: want \"N%%\" (for example 10%%) or \"Nms/Mms\"", allowance)
					}
					pol.ThrottleAllowance, set = allowance, true
				}
				if flags.Changed("ttl") {
