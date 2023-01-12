package threatdetect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/app"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// MiningThresholdsConfigKey is the DaemonConfigStore key the operator's
// mining-behavior thresholds are persisted under, as protojson of
// pb.MiningBehaviorThresholds (#2635). #2636's ProcessPolicy wraps the same
// message.
const MiningThresholdsConfigKey = "threatdetect.mining_behavior.thresholds"

// Mining-behavior defaults (PRD story 2, decided on #2641 Q1).
const (
	DefaultMiningMinCores           = 1.5
	DefaultMiningMinFractionOfLimit = 0.75
	DefaultMiningWindow             = 5 * time.Minute
	DefaultMiningFlowMinAge         = 3 * time.Minute
)

// DefaultMiningStratumPorts are the destination ports treated as stratum
// (mining pool) ports when the operator has not set a list.
var DefaultMiningStratumPorts = []uint32{3333, 4444, 5555, 7777, 14444, 14433}

// MiningThresholds is the resolved (defaults applied) form of
// pb.MiningBehaviorThresholds the mining-behavior rule evaluates against.
type MiningThresholds struct {
	MinCores           float64
	MinFractionOfLimit float64
	Window             time.Duration
	FlowMinAge         time.Duration
	StratumPorts       []uint32
}

// DefaultMiningThresholds returns the shipped defaults.
func DefaultMiningThresholds() MiningThresholds {
	return MiningThresholds{
		MinCores:           DefaultMiningMinCores,
		MinFractionOfLimit: DefaultMiningMinFractionOfLimit,
		Window:             DefaultMiningWindow,
		FlowMinAge:         DefaultMiningFlowMinAge,
		StratumPorts:       slices.Clone(DefaultMiningStratumPorts),
	}
}

// MiningThresholdsFromProto resolves a stored message: every zero or empty
// field falls back to its default, so a partial message tunes only what it
// sets. nil yields the defaults.
func MiningThresholdsFromProto(m *pb.MiningBehaviorThresholds) MiningThresholds {
	t := DefaultMiningThresholds()
	if v := m.GetMinCores(); v > 0 {
		t.MinCores = v
	}
	if v := m.GetMinFractionOfLimit(); v > 0 {
		t.MinFractionOfLimit = v
	}
	if d := m.GetWindow().AsDuration(); m.GetWindow() != nil && d > 0 {
		t.Window = d
	}
	if d := m.GetFlowMinAge().AsDuration(); m.GetFlowMinAge() != nil && d > 0 {
		t.FlowMinAge = d
	}
	if ports := m.GetStratumPorts(); len(ports) > 0 {
		t.StratumPorts = slices.Clone(ports)
	}
	return t
}

// Bar is the mean-cores level a box must reach: min(MinCores,
// MinFractionOfLimit x limitCores), or MinCores alone when the box has no
// CPU limit (limitCores <= 0).
func (t MiningThresholds) Bar(limitCores float64) float64 {
	if limitCores <= 0 {
		return t.MinCores
	}
	return min(t.MinCores, t.MinFractionOfLimit*limitCores)
}

func (t MiningThresholds) isStratumPort(port uint16) bool {
	return slices.Contains(t.StratumPorts, uint32(port))
}

// ConfigKV is the slice of app.DaemonConfigStore the thresholds config uses.
type ConfigKV interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}
