package threatdetect

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/footprintai/containarium/internal/app"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ProcessPolicy (#2636) says what a HIGH mining finding does to its
// container. The pure parts live here — resolution, validation, and the
// DaemonConfigStore-backed store; the responder that acts on it is
// internal/server/process_responder.go. See
// docs/architecture/process-exec-guardrail.md, component 7.

const (
	// ProcessPolicyKey is the DaemonConfigStore key of the backend default,
	// stored as protojson.
	ProcessPolicyKey = "threatdetect.process_policy"
	// processPolicyContainerKeyPrefix prefixes one container's override key.
	processPolicyContainerKeyPrefix = "threatdetect.process_policy.container."

	// DefaultThrottleAllowance is what THROTTLE applies when no level sets one:
	// 10% of one core, as a hard quota (10ms/100ms).
	DefaultThrottleAllowance = "10%"
	// DefaultProcessActionTTL is how long an applied action stays in effect
	// with no operator decision — the same 24h backstop auto_quarantine.go
	// uses.
	DefaultProcessActionTTL = 24 * time.Hour
	// minProcessActionTTL rejects a TTL so short the operator could never
	// reach the box before the backstop undid the action.
	minProcessActionTTL = time.Minute
)

// ProcessPolicyContainerKey is the DaemonConfigStore key of container's
// override.
func ProcessPolicyContainerKey(container string) string {
	return processPolicyContainerKeyPrefix + container
}

// BuiltinProcessPolicy is the policy in effect when nothing is stored:
// ALERT, and no process kill. Nothing acts until an operator sets otherwise;
// kill_process defaults off everywhere (decided on cloud#2697 for #2638).
func BuiltinProcessPolicy() *pb.ProcessPolicy {
	return &pb.ProcessPolicy{
		Action:            pb.ProcessAction_PROCESS_ACTION_ALERT,
		ThrottleAllowance: DefaultThrottleAllowance,
		Ttl:               durationpb.New(DefaultProcessActionTTL),
		KillProcess:       proto.Bool(false),
	}
}

// ResolveProcessPolicy merges container override → backend default →
// built-in, field by field: an empty field at one level inherits the next.
// Either argument may be nil (nothing stored at that level).
func ResolveProcessPolicy(override, backend *pb.ProcessPolicy) *pb.ProcessPolicy {
	out := BuiltinProcessPolicy()
	for _, level := range []*pb.ProcessPolicy{backend, override} {
		if level == nil {
			continue
		}
		if level.GetAction() != pb.ProcessAction_PROCESS_ACTION_UNSPECIFIED {
			out.Action = level.GetAction()
		}
		if level.GetThrottleAllowance() != "" {
			out.ThrottleAllowance = level.GetThrottleAllowance()
		}
		if d := level.GetTtl().AsDuration(); level.GetTtl() != nil && d > 0 {
			out.Ttl = durationpb.New(d)
		}
		// kill_process has presence (proto3 optional): an unset level
		// inherits, an explicit false at a higher level turns it back off.
		if level.KillProcess != nil {
			out.KillProcess = proto.Bool(level.GetKillProcess())
		}
	}
	return out
}

var (
	allowancePercentRe = regexp.MustCompile(`^([0-9]{1,3})%$`)
	allowanceQuotaRe   = regexp.MustCompile(`^([0-9]{1,9})ms/([0-9]{1,9})ms$`)
)

// ValidateThrottleAllowance accepts "N%" (1–100: N percent of one core, applied
// as the hard quota "Nms/100ms", see HardThrottleAllowance) and "Nms/Mms" (a
// hard CFS quota, both positive).
func ValidateThrottleAllowance(s string) error {
	if m := allowancePercentRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > 100 {
			return fmt.Errorf("throttle_allowance %q: percent must be between 1%% and 100%%", s)
		}
		return nil
	}
	if m := allowanceQuotaRe.FindStringSubmatch(s); m != nil {
		q, _ := strconv.Atoi(m[1])
		p, _ := strconv.Atoi(m[2])
		if q < 1 || p < 1 {
			return fmt.Errorf("throttle_allowance %q: quota and period must be positive", s)
		}
		return nil
	}
	return fmt.Errorf("throttle_allowance %q: want \"N%%\" or \"Nms/Mms\"", s)
}

// ValidateProcessPolicy rejects a policy no level could apply. Empty fields
// are valid (they inherit); only set fields are checked.
func ValidateProcessPolicy(p *pb.ProcessPolicy) error {
	if p == nil {
		return errors.New("policy is required")
	}
	if _, known := pb.ProcessAction_name[int32(p.GetAction())]; !known {
		return fmt.Errorf("action %d is not a known ProcessAction", int32(p.GetAction()))
	}
	if a := p.GetThrottleAllowance(); a != "" {
		if err := ValidateThrottleAllowance(a); err != nil {
			return err
		}
	}
	if p.GetTtl() != nil {
		d := p.GetTtl().AsDuration()
		if d < 0 {
			return fmt.Errorf("ttl %v must not be negative", d)
		}
		if d > 0 && d < minProcessActionTTL {
			return fmt.Errorf("ttl %v must be at least %v", d, minProcessActionTTL)
		}
	}
	return nil
}

// RespondsTo reports whether a HIGH finding from rule is one the process
// responder acts on: the two mining rules.
func RespondsTo(rule pb.ThreatRuleId) bool {
	return rule == pb.ThreatRuleId_THREAT_RULE_ID_MINING_BEHAVIOR ||
		rule == pb.ThreatRuleId_THREAT_RULE_ID_MINER_SIGNATURE
}

// KillsProcess reports whether a HIGH finding from rule may SIGKILL its
// process under kill_process: the signature rule only. The mining-behavior
// rule names no process (its evidence is CPU and a flow), so it never kills.
func KillsProcess(rule pb.ThreatRuleId) bool {
	return rule == pb.ThreatRuleId_THREAT_RULE_ID_MINER_SIGNATURE
}

// PolicyConfigStore is the slice of *app.DaemonConfigStore the policy store
// needs: Get returns an error wrapping app.ErrDaemonConfigNotFound for a
// missing key, exactly as DaemonConfigStore.Get does.
type PolicyConfigStore interface {
