package events

import (
	"sync"

	"github.com/google/uuid"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const (
	// DefaultChannelBufferSize is the default buffer size for subscriber channels
	DefaultChannelBufferSize = 100
)

// Subscriber represents a client subscribed to events
type Subscriber struct {
	// ID is the unique identifier for this subscriber
	ID string

	// Events is the channel where events are sent
	Events chan *pb.Event

	// Filter contains the subscription filter options
	Filter *pb.SubscribeEventsRequest

