package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/anonbox"
	"github.com/footprintai/containarium/internal/events"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// anonFunnelSink turns every anonbox funnel step into (a) an
// EVENT_TYPE_ANON_* event on the daemon's event bus, for subscribers such
// as the cloud control plane, and (b) an OTel counter
// containarium.anon.<step>_total — plus the time-to-shell histogram — so
// the PRD's three metrics are one VictoriaMetrics query each (#2201).
// The same call site feeds both, so they can never disagree.
type anonFunnelSink struct {
	bus      *events.Bus
	counters map[anonbox.FunnelKind]otelmetric.Int64Counter
	shell    otelmetric.Float64Histogram
}

// anonFunnelEventTypes maps a step to its proto event type.
var anonFunnelEventTypes = map[anonbox.FunnelKind]pb.EventType{
	anonbox.FunnelConnect:           pb.EventType_EVENT_TYPE_ANON_CONNECT,
	anonbox.FunnelShellReady:        pb.EventType_EVENT_TYPE_ANON_SHELL_READY,
	anonbox.FunnelReconnect:         pb.EventType_EVENT_TYPE_ANON_RECONNECT,
	anonbox.FunnelClaimLinkIssued:   pb.EventType_EVENT_TYPE_ANON_CLAIM_LINK_ISSUED,
	anonbox.FunnelClaimCompleted:    pb.EventType_EVENT_TYPE_ANON_CLAIM_COMPLETED,
	anonbox.FunnelExpired:           pb.EventType_EVENT_TYPE_ANON_EXPIRED,
	anonbox.FunnelKilledAbuse:       pb.EventType_EVENT_TYPE_ANON_KILLED_ABUSE,
	anonbox.FunnelRejectedCapacity:  pb.EventType_EVENT_TYPE_ANON_REJECTED_CAPACITY,
	anonbox.FunnelRejectedRateLimit: pb.EventType_EVENT_TYPE_ANON_REJECTED_RATELIMIT,
	anonbox.FunnelRejectedDoor:      pb.EventType_EVENT_TYPE_ANON_REJECTED_DOOR,
	anonbox.FunnelReminderOptIn:     pb.EventType_EVENT_TYPE_ANON_REMINDER_OPTIN,
}

// newAnonFunnelSink registers one counter per step on mp's "containarium"
// meter, named containarium.anon.<step>_total (rendered by the
// collector → VictoriaMetrics path as containarium_anon_<step>_total), and
// the containarium.anon.time_to_shell_seconds histogram.
