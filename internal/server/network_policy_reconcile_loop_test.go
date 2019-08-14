package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// countingInspector counts ListContainers calls: one Incus container list is
// what each reconcile pass costs (gather), so the count is the pass's Incus
// call volume in units of "one full list".
type countingInspector struct{ lists atomic.Int32 }

func (c *countingInspector) ListContainers() ([]incus.ContainerInfo, error) {
	c.lists.Add(1)
	return nil, nil
}

func (c *countingInspector) GetRawInstance(string) (map[string]string, string, error) {
	return nil, "", nil
}

func (c *countingInspector) GetRawInstanceDevices(string) (map[string]map[string]string, error) {
	return nil, nil
}

// loopEnforcer is an enforcer whose reconcile pass is one ListContainers on
// insp (what gather does), driven by the real loop and bus subscription.
func loopEnforcer(t *testing.T, bus *events.Bus, interval, debounce time.Duration) (*NetworkPolicyEnforcer, *countingInspector) {
	t.Helper()
	insp := &countingInspector{}
	e := NewNetworkPolicyEnforcer("", nil, NewMemTenantRegistry(), insp, nil, bus, false)
	e.interval = interval
	e.eventDebounce = debounce
	e.pass = func(context.Context) error { _, err := insp.ListContainers(); return err }
	e.ctx, e.cancel = context.WithCancel(context.Background())
	kick := containerTopologyKick(e.ctx, bus, e.eventDebounce)
	e.wg.Add(1)
	go e.reconcileLoop(kick)
	t.Cleanup(func() { e.cancel(); e.wg.Wait() })
	return e, insp
}

// emitNonContainerEvents publishes one of every event kind the bus carries
// that does not change container topology: metrics, traffic, security
// findings, app and route events, and an anonymous-box funnel event.
func emitNonContainerEvents(bus *events.Bus) {
	em := events.NewEmitter(bus)
	em.EmitMetricsUpdate(nil)
	em.EmitTrafficEvent(&pb.TrafficEvent{Connection: &pb.Connection{ContainerName: "a-box"}})
	em.EmitSecurityFinding(&pb.Finding{TenantId: "t"})
	em.EmitAppDeployed(&pb.App{Id: "app"})
	em.EmitAppStateChanged(&pb.App{Id: "app"}, pb.AppState_APP_STATE_RUNNING)
	em.EmitRouteAdded(&pb.ProxyRoute{FullDomain: "app.example.com"})
	bus.Publish(&pb.Event{Type: pb.EventType_EVENT_TYPE_ANON_CONNECT, ResourceType: pb.ResourceType_RESOURCE_TYPE_ANON_BOX})
}

// #3050: a steady stream of non-container events (traffic, findings, ...) must
// not drive reconcile passes; the Incus list volume stays at the timer
// baseline. Before the fix every such event ran a full pass, back to back.
func TestReconcileLoop_NonContainerEventStreamStaysAtTimerBaseline(t *testing.T) {
	bus := events.NewBus()
	const interval = 200 * time.Millisecond
	const run = 1100 * time.Millisecond
	_, insp := loopEnforcer(t, bus, interval, 20*time.Millisecond)

	deadline := time.Now().Add(run)
	for time.Now().Before(deadline) {
		emitNonContainerEvents(bus)
		time.Sleep(time.Millisecond)
	}

	baseline := int32(run / interval) // timer ticks in the window
	if got := insp.lists.Load(); got > baseline+1 {
		t.Fatalf("Incus container lists = %d over %s of non-container events, want at most the timer baseline %d (+1)", got, run, baseline)
	}
}

// #3050: a burst of container topology events produces one pass, after the
// trailing debounce, not one pass per event.
func TestReconcileLoop_ContainerEventBurstIsOneDebouncedPass(t *testing.T) {
	bus := events.NewBus()
	const debounce = 150 * time.Millisecond
	_, insp := loopEnforcer(t, bus, time.Hour, debounce) // timer out of the picture

	em := events.NewEmitter(bus)
	c := &pb.Container{Name: "a-box"}
	for i := 0; i < 5; i++ {
		em.EmitContainerCreated(c)
		em.EmitContainerStarted(c)
		em.EmitContainerStateChanged(c, pb.ContainerState_CONTAINER_STATE_STOPPED)
		em.EmitContainerStopped(c)
		em.EmitContainerDeleted(c.Name)
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(debounce / 3)
	if got := insp.lists.Load(); got != 0 {
		t.Fatalf("passes before the debounce elapsed = %d, want 0 (events still arriving %s ago)", got, debounce/3)
	}
	time.Sleep(debounce * 3)
