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
