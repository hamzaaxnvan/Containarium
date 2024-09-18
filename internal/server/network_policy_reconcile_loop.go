package server

import (
	"context"
	"log"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// defaultContainerEventDebounce is the trailing debounce on the event-driven
// reconcile path (#3050): a burst of container events produces one pass, this
// long after the last event of the burst, so passes cannot run back to back.
const defaultContainerEventDebounce = 2 * time.Second

// containerTopologyEvents are the bus events that change which boxes exist,
// run, or hold which address: the only ones a topology reconciler needs to
// wake early for (#3050). Metrics, traffic, security-finding, app, route and
// anonymous-box events do not change topology; before #3050 the traffic and
// finding streams alone ran full reconcile passes back to back.
var containerTopologyEvents = map[pb.EventType]bool{
	pb.EventType_EVENT_TYPE_CONTAINER_CREATED:       true,
	pb.EventType_EVENT_TYPE_CONTAINER_DELETED:       true,
	pb.EventType_EVENT_TYPE_CONTAINER_STARTED:       true,
	pb.EventType_EVENT_TYPE_CONTAINER_STOPPED:       true,
	pb.EventType_EVENT_TYPE_CONTAINER_STATE_CHANGED: true,
}

// containerTopologyKick subscribes to bus and returns a channel that receives
// one signal per burst of container topology events, debounce after the
// burst's last event (a trailing debounce). A signal already pending absorbs
// later bursts. It returns a nil channel (which blocks forever in a select)
// when bus is nil. The subscription is made before it returns and ends when
// ctx is done.
func containerTopologyKick(ctx context.Context, bus *events.Bus, debounce time.Duration) <-chan struct{} {
	if bus == nil {
		return nil
	}
	// The bus filters by resource type; the event type is checked below.
	sub := bus.Subscribe(&pb.SubscribeEventsRequest{
		ResourceTypes: []pb.ResourceType{pb.ResourceType_RESOURCE_TYPE_CONTAINER},
	})
	kick := make(chan struct{}, 1)
	go func() {
		defer bus.Unsubscribe(sub.ID)
		timer := time.NewTimer(debounce)
		timer.Stop()
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub.Events:
				if !ok {
					return
				}
				if !containerTopologyEvents[ev.GetType()] {
					continue
				}
				timer.Reset(debounce) // trailing: restart on every event of the burst
			case <-timer.C:
