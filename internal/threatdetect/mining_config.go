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
