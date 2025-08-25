package audit

import (
	"context"
	"log"
	"time"

	"github.com/footprintai/containarium/internal/events"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// EventSubscriber subscribes to the global event bus and persists events as audit log entries
type EventSubscriber struct {
	bus    *events.Bus
	store  *Store
	cancel context.CancelFunc
	done   chan struct{}
}

// NewEventSubscriber creates a new event subscriber
func NewEventSubscriber(bus *events.Bus, store *Store) *EventSubscriber {
	return &EventSubscriber{
		bus:   bus,
		store: store,
		done:  make(chan struct{}),
