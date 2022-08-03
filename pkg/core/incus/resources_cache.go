package incus

import (
	"errors"
	"sync"
	"time"
)

// Bounded, cached system-resource reads (#2325).
//
// Incus' hardware-discovery call (GET /1.0/resources, behind GetServerResources) can stop answering on a heavily
// loaded host. The daemon used to read it through a client with no timeout from the create path (CPU admission),
// GetSystemInfo, ListBackends and the metrics collectors, so every one of them blocked for as long as Incus did: a
// hung scan stopped container creation and the fleet view, silently.
//
// The values it feeds are mostly hardware: the logical CPU count, CPU model, total memory, total disk and GPUs do
// not change while the daemon runs. So the read is cached and bounded:
//
//   - one shared in-flight read, so concurrent callers cost one request and a hung Incus cannot accumulate requests;
//   - the last good value is served as is for a short freshness window;
//   - when a refresh fails or times out, the last good value keeps being served (stale), so the hardware-static
//     fields, notably the CPU count the admission gate needs, stay available through an outage;
//   - after a failure or timeout, callers do not retry for a short back-off, so a hung Incus costs one caller per
//     back-off window the wait budget instead of every create;
//   - if there has never been a successful read, the caller gets an error within the wait budget.
//
// The load averages come from /proc/loadavg, not from Incus, so they are overlaid fresh on every return.
const (
	// resourcesRequestTimeout bounds each HTTP request of one read (resources, pool list, each pool's usage).
	resourcesRequestTimeout = 8 * time.Second
	// resourcesFreshFor is how long a successful read is served without refetching.
	resourcesFreshFor = 10 * time.Second
	// resourcesWait is the longest a caller waits for an in-flight read.
	resourcesWait = 8 * time.Second
	// resourcesBackoff is how long after a failed or timed-out read callers are served the cached value (or an
	// immediate error) instead of waiting on Incus again.
	resourcesBackoff = 20 * time.Second
)

// ErrResourcesUnavailable is returned when Incus did not answer the resources read within the wait budget and no
// earlier value is cached.
var ErrResourcesUnavailable = errors.New("incus: system resources unavailable (read timed out or failed)")

type resourcesCacheConfig struct {
	Fresh, Wait, Backoff time.Duration
	Now                  func() time.Time
}

type resFlight struct {
	done chan struct{}
	val  *SystemResources
	err  error
}

// resourcesCache is the single-flight, last-good, back-off cache described above. Safe for concurrent use.
type resourcesCache struct {
	cfg   resourcesCacheConfig
	fetch func() (*SystemResources, error)

	mu       sync.Mutex
	val      *SystemResources // last good read
	at       time.Time
	failedAt time.Time // zero when the last attempt succeeded
	lastErr  error
	flight   *resFlight
}

func newResourcesCache(fetch func() (*SystemResources, error), cfg resourcesCacheConfig) *resourcesCache {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &resourcesCache{cfg: cfg, fetch: fetch}
}

func cloneResources(v *SystemResources) *SystemResources {
	if v == nil {
		return nil
	}
	c := *v
	c.GPUs = append([]GPUInfo(nil), v.GPUs...)
	return &c
}

// withFreshLoad copies v and overlays the current load averages (a local /proc read, never an Incus call).
func withFreshLoad(v *SystemResources) *SystemResources {
	c := cloneResources(v)
	if l1, l5, l15, err := getCPULoadAvg(); err == nil {
		c.CPULoad1Min, c.CPULoad5Min, c.CPULoad15Min = l1, l5, l15
	}
	return c
}

// get returns the system resources, bounded: it never blocks longer than cfg.Wait.
func (rc *resourcesCache) get() (*SystemResources, error) {
	rc.mu.Lock()
	now := rc.cfg.Now()
	if rc.val != nil && now.Sub(rc.at) < rc.cfg.Fresh {
		v := rc.val
		rc.mu.Unlock()
		return withFreshLoad(v), nil
	}
	if !rc.failedAt.IsZero() && now.Sub(rc.failedAt) < rc.cfg.Backoff {
		v, err := rc.val, rc.lastErr
		rc.mu.Unlock()
		return staleOrErr(v, err)
	}
	f := rc.flight
	if f == nil {
		f = &resFlight{done: make(chan struct{})}
		rc.flight = f
		go rc.run(f)
	}
	rc.mu.Unlock()

	timer := time.NewTimer(rc.cfg.Wait)
	defer timer.Stop()
	select {
	case <-f.done:
