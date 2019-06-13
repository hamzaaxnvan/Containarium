package traffic

import (
	"errors"
	"time"
)

// ErrNotSupported is returned when conntrack is not supported on this platform
var ErrNotSupported = errors.New("conntrack monitoring is only supported on Linux")

// ConntrackEventType represents the type of conntrack event
type ConntrackEventType int

const (
	// ConntrackEventNew indicates a new connection
	ConntrackEventNew ConntrackEventType = iota
	// ConntrackEventUpdate indicates a connection update (state/counters)
	ConntrackEventUpdate
	// ConntrackEventDestroy indicates a connection was terminated
	ConntrackEventDestroy
)

// String returns the string representation of the event type
func (t ConntrackEventType) String() string {
	switch t {
	case ConntrackEventNew:
		return "NEW"
	case ConntrackEventUpdate:
		return "UPDATE"
	case ConntrackEventDestroy:
		return "DESTROY"
	default:
		return "UNKNOWN"
	}
}

// ConntrackEvent represents a connection tracking event
type ConntrackEvent struct {
	// ID is a unique identifier for this connection
	ID string

	// Type indicates the event type (new, update, destroy)
	Type ConntrackEventType

	// Protocol is the connection protocol (tcp, udp, icmp)
	Protocol string
