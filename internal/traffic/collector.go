package traffic

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/network"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// CollectorConfig holds configuration for the traffic collector
type CollectorConfig struct {
	// NetworkCIDR is the container network CIDR (e.g., "10.100.0.0/24")
	NetworkCIDR string

	// SnapshotInterval is how often to take a full conntrack snapshot
	SnapshotInterval time.Duration

	// CleanupInterval is how often to run database cleanup
	CleanupInterval time.Duration

	// RetentionDays is how many days to keep traffic data
	RetentionDays int

	// PostgresConnString is the database connection string
	PostgresConnString string
}

// DefaultCollectorConfig returns a default configuration
func DefaultCollectorConfig() CollectorConfig {
	return CollectorConfig{
		NetworkCIDR:      "10.100.0.0/24",
		SnapshotInterval: 5 * time.Minute,
		CleanupInterval:  24 * time.Hour,
		RetentionDays:    7,
	}
}

// Collector coordinates traffic monitoring
type Collector struct {
	config      CollectorConfig
	incusClient *incus.Client
	store       *Store
	cache       *ContainerCache
	monitor     ConntrackMonitor
	emitter     *events.Emitter

	mu          sync.RWMutex
	connections map[string]*pb.Connection // conntrack ID -> connection
	ebpfFlows   map[string]*pb.Connection // eBPF flow ID -> connection (#627)
	// conntrackSeen is the set of container names the conntrack collector has
	// successfully attributed at least one flow for. It's the source-arbitration
	// signal for #643: where conntrack works for a container, it owns that
	// container's traffic (live view + history), so the eBPF flow data is
	// suppressed to avoid double-counting; where it doesn't (docker-in-LXC,
	// masquerade — conntrack never attributes the container), eBPF is the only
	// source and is used. Container-granularity, so there's no per-flow timing
	// race. Sticky: once true it stays true (attribution is by container IP,
	// all-or-nothing per container).
	conntrackSeen map[string]bool

	ctx    context.Context
	cancel context.CancelFunc
}

// NewCollector creates a new traffic collector
func NewCollector(config CollectorConfig, incusClient *incus.Client, store *Store, emitter *events.Emitter) (*Collector, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize cache
	cache := NewContainerCache(incusClient, config.NetworkCIDR)

	// Initialize conntrack monitor
	monitor, err := NewConntrackMonitor()
	if err != nil {
		cancel()
		// Don't fail if conntrack is not available (e.g., on macOS)
		log.Printf("Warning: conntrack monitoring unavailable: %v", err)
		monitor = nil
	}

	return &Collector{
		config:        config,
		incusClient:   incusClient,
		store:         store,
		cache:         cache,
		monitor:       monitor,
		emitter:       emitter,
		connections:   make(map[string]*pb.Connection),
		ebpfFlows:     make(map[string]*pb.Connection),
		conntrackSeen: make(map[string]bool),
		ctx:           ctx,
		cancel:        cancel,
	}, nil
}

// Start begins traffic collection
func (c *Collector) Start() error {
	log.Printf("Starting traffic collector for network %s", c.config.NetworkCIDR)

	// Enable conntrack accounting for byte counters (Linux only)
	if c.monitor != nil {
		if err := network.EnableConntrackAccounting(); err != nil {
			log.Printf("Warning: failed to enable conntrack accounting: %v", err)
		}
	}

	// Start container cache refresh
	go c.cache.StartRefresh(c.ctx, 30*time.Second)

	// Start conntrack event monitoring (if available)
	if c.monitor != nil {
		go c.handleConntrackEvents()
	}

	// Start periodic snapshot
	go c.periodicSnapshot()

	// Start periodic cleanup
	if c.store != nil {
		go c.periodicCleanup()
	}

	return nil
}

// handleConntrackEvents processes real-time conntrack events
func (c *Collector) handleConntrackEvents() {
	eventCh := c.monitor.Events()

	for {
		select {
		case <-c.ctx.Done():
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			c.processConntrackEvent(event)
		}
	}
}

// processConntrackEvent handles a single conntrack event
func (c *Collector) processConntrackEvent(event *ConntrackEvent) {
	// Determine which container this connection belongs to
	containerName := ""
	containerIP := ""
	direction := pb.TrafficDirection_TRAFFIC_DIRECTION_UNSPECIFIED

	// Check source IP first (egress from container)
	if name := c.cache.LookupIP(event.SrcIP); name != "" {
		containerName = name
		containerIP = event.SrcIP
		direction = pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS
	} else if name := c.cache.LookupIP(event.DstIP); name != "" {
		// Check destination IP (ingress to container)
		containerName = name
		containerIP = event.DstIP
		direction = pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS
	}

	// Skip if not a container connection
	if containerName == "" {
		return
	}

	// Convert to proto connection
	conn := c.convertToProto(event, containerName, containerIP, direction)

	// Update local cache
	c.mu.Lock()
	c.conntrackSeen[containerName] = true // conntrack owns this container's history (#643)
	if event.Type == ConntrackEventDestroy {
		delete(c.connections, event.ID)
	} else {
		c.connections[event.ID] = conn
	}
	c.mu.Unlock()

	// Emit traffic event
	c.emitTrafficEvent(event.Type, conn)

	// Persist to database on connection close
	if event.Type == ConntrackEventDestroy && c.store != nil {
		go func() {
			if err := c.store.SaveConnection(c.ctx, conn); err != nil {
				log.Printf("Warning: failed to persist connection: %v", err)
			}
		}()
	}
}

// convertToProto converts a ConntrackEvent to a pb.Connection
func (c *Collector) convertToProto(event *ConntrackEvent, containerName, containerIP string, direction pb.TrafficDirection) *pb.Connection {
	conn := &pb.Connection{
		Id:             event.ID,
		ContainerName:  containerName,
		ContainerIp:    containerIP,
		Protocol:       protoStringToEnum(event.Protocol),
		SourceIp:       event.SrcIP,
		SourcePort:     uint32(event.SrcPort),
		DestIp:         event.DstIP,
		DestPort:       uint32(event.DstPort),
		State:          stateStringToEnum(event.State),
		Direction:      direction,
		FirstSeen:      timestamppb.New(event.Timestamp),
		LastSeen:       timestamppb.New(event.Timestamp),
		TimeoutSeconds: event.Timeout,
	}

	// Set bytes based on direction
	if direction == pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS {
		conn.BytesSent = event.BytesOrig
		conn.BytesReceived = event.BytesReply
		conn.PacketsSent = event.PacketsOrig
		conn.PacketsReceived = event.PacketsReply
	} else {
		conn.BytesSent = event.BytesReply
		conn.BytesReceived = event.BytesOrig
		conn.PacketsSent = event.PacketsReply
		conn.PacketsReceived = event.PacketsOrig
	}

	return conn
}

// EBPFFlow is one per-flow accounting record sourced from the eBPF per-veth
// network-policy program (issue #627). Bytes/Packets are the container's EGRESS
// (container→peer) seen on the host-veth ingress hook; RxBytes/RxPackets are the
// reply direction (peer→container) seen on the veth egress hook (#631). RxBytes/
// RxPackets are 0 when the loaded BPF object predates #631 (no egress program).
type EBPFFlow struct {
	ContainerName string
	ContainerIP   string
	Protocol      string // "tcp" | "udp" | "icmp"
	SrcIP         string
	SrcPort       uint16
	DstIP         string
	DstPort       uint16
	Bytes         int64 // sent: container → peer (veth ingress hook)
	Packets       int64
	RxBytes       int64 // received: peer → container (veth egress hook, #631); 0 if unavailable
	RxPackets     int64
	First         time.Time
	Last          time.Time
