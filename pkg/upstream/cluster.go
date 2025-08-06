// Package upstream provides a minimal reverse‑proxy cluster with passive and
// optional active health checks. The hot path is completely lock‑free and
// allocation‑free; all mutations use copy‑on‑write under a coarse RW lock.
package upstream

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/rs/zerolog/log"

	"github.com/Borislavv/advanced-cache/pkg/config"
)

// -----------------------------------------------------------------------------
// Errors exposed by the package.
// -----------------------------------------------------------------------------

var (
	ErrNoBackends   = errors.New("no healthy backends in cluster")
	ErrDuplicate    = errors.New("backend already exists in cluster")
	ErrNotFound     = errors.New("backend not found in cluster")
	ErrNilBackend   = errors.New("nil backend")
	ErrNameMismatch = errors.New("backend name mismatch")
)

// -----------------------------------------------------------------------------
// Metrics (atomic gauges).
// -----------------------------------------------------------------------------

var (
	healthyGauge atomic.Int64
	sickGauge    atomic.Int64
	deadGauge    atomic.Int64
)

func HealthyBackends() int64 { return healthyGauge.Load() }
func SickBackends() int64    { return sickGauge.Load() }
func DeadBackends() int64    { return deadGauge.Load() }

// -----------------------------------------------------------------------------
// Internal state.
// -----------------------------------------------------------------------------

type state uint8

const (
	_ state = iota
	healthy
	sick
	dead
)

type backendSlot struct {
	backend Backend
	st      state
}

// -----------------------------------------------------------------------------
// BackendsCluster – round‑robin balancer.
// -----------------------------------------------------------------------------

type BackendsCluster struct {
	cfg config.Config

	cursor   atomic.Uint64                  // RR cursor (overflow‑safe)
	slotsPtr atomic.Pointer[[]*backendSlot] // immutable slice of healthy backends

	mu   sync.RWMutex            // guards maps & slice rebuilds
	all  map[string]*backendSlot // master registry by name
	sick map[string]*backendSlot // quarantine set
	dead map[string]*backendSlot // permanently removed

	hcTicker  *time.Ticker
	hcStopCh  chan struct{}
	hcWG      sync.WaitGroup
	hcEnabled bool
}

// NewCluster builds a cluster from cfg. Active HC starts automatically when
// at least one backend supports Ping()/IsHealthy().
func NewCluster(ctx context.Context, cfg config.Config) (*BackendsCluster, error) {
	c := &BackendsCluster{
		cfg:       cfg,
		all:       make(map[string]*backendSlot),
		sick:      make(map[string]*backendSlot),
		dead:      make(map[string]*backendSlot),
		hcStopCh:  make(chan struct{}),
		hcEnabled: true,
	}

	gofakeit.Seed(time.Now().UnixNano())
	reserved := make(map[string]struct{}, len(cfg.Upstream().Cluster.Backends))

	var healthySlots []*backendSlot
	for _, bcfg := range cfg.Upstream().Cluster.Backends {
		name := bcfg.Name
		if name == "" {
			for i := 0; i < 3; i++ {
				cand := gofakeit.FirstName()
				if _, dup := reserved[cand]; !dup {
					name = cand
					reserved[name] = struct{}{}
					break
				}
			}
		}

		be := NewBackend(ctx, bcfg, name)
		slot := &backendSlot{backend: be}
		if be.IsHealthy() {
			slot.st = healthy
			healthySlots = append(healthySlots, slot)
		} else {
			slot.st = sick
			c.sick[name] = slot
		}
		c.all[name] = slot
	}

	if len(healthySlots) == 0 {
		return nil, ErrNoBackends
	}

	c.setHealthySlice(healthySlots)

	healthyGauge.Store(int64(len(healthySlots)))
	sickGauge.Store(int64(len(c.sick)))
	deadGauge.Store(0)

	if c.hcEnabled {
		c.startHealthChecker()
	}
	return c, nil
}

// -----------------------------------------------------------------------------
// Traffic path – lock & alloc free.
// -----------------------------------------------------------------------------

func (c *BackendsCluster) Fetch(rule *config.Rule, path, query []byte, qh *[][2][]byte) (int, *[][2][]byte, []byte, func(), error) {
	slots := c.slotsPtr.Load()
	if len(*slots) == 0 {
		return 0, nil, nil, emptyReleaseFn, ErrNoBackends
	}

	idx := int(c.cursor.Add(1) % uint64(len(*slots)))
	slot := (*slots)[idx]

	st, hdr, body, rel, err := slot.backend.Fetch(rule, path, query, qh)

	if err != nil || st >= http.StatusInternalServerError {
		go c.markSickAsync(slot.backend.Name())
	}
	return st, hdr, body, rel, err
}

// -----------------------------------------------------------------------------
// Mutation API.
// -----------------------------------------------------------------------------

// Add registers a backend at runtime. Sick backends are allowed – they start in quarantine.
func (c *BackendsCluster) Add(be Backend) error {
	if be == nil {
		return ErrNilBackend
	}
	name := be.Name()

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, dup := c.all[name]; dup {
		return ErrDuplicate
	}

	slot := &backendSlot{backend: be}
	c.all[name] = slot

	if be.IsHealthy() {
		slot.st = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)
	} else {
		slot.st = sick
		c.sick[name] = slot
		sickGauge.Add(1)
	}
	return nil
}

// Update swaps backend implementation keeping the same logical name.
func (c *BackendsCluster) Update(ctx context.Context, name string, cfg *config.Backend) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[name]
	if !ok {
		return ErrNotFound
	}

	// Build new backend from cfg (if supplied) or refresh existing.
	var be Backend
	if cfg != nil {
		be = NewBackend(ctx, cfg, name)
	} else {
		be = slot.backend // keep existing instance
	}

	// State transition bookkeeping.
	prevState := slot.st

	// Remove from healthy slice if it was there
	if prevState == healthy {
		c.removeFromHealthySlice(slot)
		healthyGauge.Add(-1)
	} else if prevState == sick {
		delete(c.sick, name)
		sickGauge.Add(-1)
	}

	slot.backend = be

	if be.IsHealthy() {
		slot.st = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)
	} else {
		slot.st = sick
		c.sick[name] = slot
		sickGauge.Add(1)
	}

	return nil
}

// Resurrect moves a dead backend back to life; cfg may replace the implementation.
func (c *BackendsCluster) Resurrect(ctx context.Context, name string, cfg *config.Backend) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.dead[name]
	if !ok {
		return ErrNotFound
	}

	delete(c.dead, name)
	deadGauge.Add(-1)

	// Build new backend if cfg passed.
	if cfg != nil {
		slot.backend = NewBackend(ctx, cfg, name)
	}

	if slot.backend.IsHealthy() {
		slot.st = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)
	} else {
		slot.st = sick
		c.sick[name] = slot
		sickGauge.Add(1)
	}

	c.all[name] = slot // ensure back in master map
	return nil
}

func (c *BackendsCluster) Promote(name string) error    { return c.promote(name) }
func (c *BackendsCluster) Quarantine(name string) error { return c.quarantine(name) }
func (c *BackendsCluster) Kill(name string) error       { return c.bury(name) }

// Healthy returns true when at least one backend is ready.
func (c *BackendsCluster) Healthy() bool { return len(*c.slotsPtr.Load()) > 0 }
func (c *BackendsCluster) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.all)
}

// -----------------------------------------------------------------------------
// Internal helpers.
// -----------------------------------------------------------------------------

func (c *BackendsCluster) setHealthySlice(slots []*backendSlot) {
	c.slotsPtr.Store(&slots)
	if len(slots) == 0 {
		c.cursor.Store(0)
	}
}

func (c *BackendsCluster) appendHealthySlot(slot *backendSlot) {
	old := c.slotsPtr.Load()
	newSlice := append(append([]*backendSlot(nil), *old...), slot)
	c.setHealthySlice(newSlice)
}

func (c *BackendsCluster) removeFromHealthySlice(slot *backendSlot) {
	old := c.slotsPtr.Load()
	newSlice := make([]*backendSlot, 0, len(*old)-1)
	for _, s := range *old {
		if s != slot {
			newSlice = append(newSlice, s)
		}
	}
	c.setHealthySlice(newSlice)
}

func (c *BackendsCluster) quarantine(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[name]
	if !ok {
		return ErrNotFound
	}
	if slot.st != healthy {
		return nil // already not healthy
	}

	c.removeFromHealthySlice(slot)
	healthyGauge.Add(-1)

	slot.st = sick
	c.sick[name] = slot
	sickGauge.Add(1)
	return nil
}

func (c *BackendsCluster) promote(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.sick[name]
	if !ok {
		return ErrNotFound
	}
	delete(c.sick, name)
	sickGauge.Add(-1)

	slot.st = healthy
	c.appendHealthySlot(slot)
	healthyGauge.Add(1)
	return nil
}

func (c *BackendsCluster) bury(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[name]
	if !ok {
		return ErrNotFound
	}

	switch slot.st {
	case healthy:
		c.removeFromHealthySlice(slot)
		healthyGauge.Add(-1)
	case sick:
		delete(c.sick, name)
		sickGauge.Add(-1)
	}

	slot.st = dead
	c.dead[name] = slot
	deadGauge.Add(1)
	return nil
}

func (c *BackendsCluster) markSickAsync(name string) {
	if err := c.quarantine(name); err == nil {
		log.Warn().Str("backend", name).Msg("upstream: backend quarantined due to error response")
	}
}

// -----------------------------------------------------------------------------
// Active health checker.
// -----------------------------------------------------------------------------

func (c *BackendsCluster) startHealthChecker() {
	const interval = 10 * time.Second
	c.hcTicker = time.NewTicker(interval)
	c.hcWG.Add(1)

	go func() {
		defer c.hcWG.Done()
		for {
			select {
			case <-c.hcTicker.C:
				c.probeSickBackends()
			case <-c.hcStopCh:
				return
			}
		}
	}()
}

func (c *BackendsCluster) probeSickBackends() {
	c.mu.RLock()
	candidates := make([]Backend, 0, len(c.sick))
	for _, slot := range c.sick {
		candidates = append(candidates, slot.backend)
	}
	c.mu.RUnlock()

	for _, be := range candidates {
		if be.IsHealthy() {
			_ = c.promote(be.Name())
		}
	}
}

// Close stops background HC goroutine.
func (c *BackendsCluster) Close() {
	if !c.hcEnabled {
		return
	}
	close(c.hcStopCh)
	if c.hcTicker != nil {
		c.hcTicker.Stop()
	}
	c.hcWG.Wait()
}
