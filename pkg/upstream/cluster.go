// Package upstream provides a minimal reverse‑proxy cluster with passive and
// optional active health checks. The hot path is lock‑free and allocation‑free;
// all mutations use copy‑on‑write under a coarse RW lock.
//
// Logging
// -------
// All non‑hot‑path state transitions are traced with zerolog using the common
// prefix **[upstream‑cluster]**.  Это даёт чёткую цепочку действий без шума в
// критическом трафике.
//
// Metrics
// -------
// * aggregate gauges: Healthy / Sick / Dead
// * per‑backend gauge  `backend_state{backend="name",state="healthy|sick|dead"}`
//
// Helper `SnapshotMetrics()` возвращает payload в формате Prometheus, готовый
// к push в VictoriaMetrics (`/api/v1/import/prometheus`) или scrape любым
// Prometheus‑совместимым сборщиком.
package upstream

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/rs/zerolog/log"

	"github.com/Borislavv/advanced-cache/pkg/config"
)

// -----------------------------------------------------------------------------
// Errors.
// -----------------------------------------------------------------------------

var (
	ErrNoBackends = errors.New("no healthy backends in cluster")
	ErrDuplicate  = errors.New("backend already exists in cluster")
	ErrNotFound   = errors.New("backend not found in cluster")
	ErrNilBackend = errors.New("nil backend")
)

// -----------------------------------------------------------------------------
// Aggregate metrics.
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
// Types & internal state.
// -----------------------------------------------------------------------------

type state uint8

const (
	_ state = iota
	healthy
	sick
	dead
)

func (s state) String() string {
	switch s {
	case healthy:
		return "healthy"
	case sick:
		return "sick"
	case dead:
		return "dead"
	default:
		return "unknown"
	}
}

type backendSlot struct {
	backend Backend
	st      state
}

// -----------------------------------------------------------------------------
// Cluster.
// -----------------------------------------------------------------------------

type BackendsCluster struct {
	cfg config.Config

	cursor   atomic.Uint64                  // RR cursor (overflow‑safe)
	slotsPtr atomic.Pointer[[]*backendSlot] // immutable healthy slice

	mu   sync.RWMutex            // guards maps & slice rebuilds
	all  map[string]*backendSlot // every backend by name
	sick map[string]*backendSlot // quarantined
	dead map[string]*backendSlot // removed

	hcTicker  *time.Ticker
	hcStopCh  chan struct{}
	hcWG      sync.WaitGroup
	hcEnabled bool
}

// NewCluster initialises cluster & launches active HC.
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

		be := NewBackend(ctx, bcfg, name) // реализация за пределами кластера
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
		log.Error().Msg("[upstream-cluster] init failed: no healthy backends")
		return nil, ErrNoBackends
	}

	c.setHealthySlice(healthySlots)
	healthyGauge.Store(int64(len(healthySlots)))
	sickGauge.Store(int64(len(c.sick)))

	log.Info().Int("healthy", int(HealthyBackends())).Int("sick", int(SickBackends())).
		Msg("[upstream-cluster] cluster initialised")

	if c.hcEnabled {
		c.startHealthChecker()
	}
	return c, nil
}

// -----------------------------------------------------------------------------
// Hot path (Fetch) – no logs/allocs.
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

func (c *BackendsCluster) Add(be Backend) error {
	if be == nil {
		return ErrNilBackend
	}
	name := be.Name()

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, dup := c.all[name]; dup {
		log.Warn().Str("backend", name).Msg("[upstream-cluster] add ignored: duplicate")
		return ErrDuplicate
	}

	slot := &backendSlot{backend: be}
	c.all[name] = slot

	if be.IsHealthy() {
		slot.st = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)
		log.Info().Str("backend", name).Msg("[upstream-cluster] backend added as healthy")
	} else {
		slot.st = sick
		c.sick[name] = slot
		sickGauge.Add(1)
		log.Info().Str("backend", name).Msg("[upstream-cluster] backend added as sick")
	}
	return nil
}

func (c *BackendsCluster) Update(ctx context.Context, name string, cfg *config.Backend) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[name]
	if !ok {
		return ErrNotFound
	}

	var be Backend
	if cfg != nil {
		be = NewBackend(ctx, cfg, name)
	} else {
		be = slot.backend
	}

	prev := slot.st
	if prev == healthy {
		c.removeFromHealthySlice(slot)
		healthyGauge.Add(-1)
	} else if prev == sick {
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

	log.Info().Str("backend", name).Str("from", prev.String()).Str("to", slot.st.String()).
		Msg("[upstream-cluster] backend updated")
	return nil
}

func (c *BackendsCluster) Resurrect(ctx context.Context, name string, cfg *config.Backend) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.dead[name]
	if !ok {
		return ErrNotFound
	}
	delete(c.dead, name)
	deadGauge.Add(-1)

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

	c.all[name] = slot
	log.Info().Str("backend", name).Msg("[upstream-cluster] backend resurrected")
	return nil
}

// wrappers
func (c *BackendsCluster) Promote(name string) error    { return c.promote(name) }
func (c *BackendsCluster) Quarantine(name string) error { return c.quarantine(name) }
func (c *BackendsCluster) Kill(name string) error       { return c.bury(name) }

// -----------------------------------------------------------------------------
// State transitions helpers (with metrics + logs).
// -----------------------------------------------------------------------------

func (c *BackendsCluster) quarantine(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[name]
	if !ok {
		return ErrNotFound
	}
	if slot.st != healthy {
		return nil
	}

	c.removeFromHealthySlice(slot)
	healthyGauge.Add(-1)

	slot.st = sick
	c.sick[name] = slot
	sickGauge.Add(1)

	log.Info().Str("backend", name).Msg("[upstream-cluster] backend quarantined")
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

	log.Info().Str("backend", name).Msg("[upstream-cluster] backend promoted to healthy")
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

	log.Info().Str("backend", name).Msg("[upstream-cluster] backend buried (dead)")
	return nil
}

func (c *BackendsCluster) markSickAsync(name string) {
	if err := c.quarantine(name); err == nil {
		log.Warn().Str("backend", name).Msg("[upstream-cluster] backend quarantined due to error")
	}
}

// -----------------------------------------------------------------------------
// Slice helpers (no logging).
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

// -----------------------------------------------------------------------------
// Active health checker.
// -----------------------------------------------------------------------------

func (c *BackendsCluster) startHealthChecker() {
	const interval = 10 * time.Second
	c.hcTicker = time.NewTicker(interval)
	c.hcWG.Add(1)

	log.Info().Dur("interval", interval).Msg("[upstream-cluster] active HC started")

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

func (c *BackendsCluster) Close() {
	if !c.hcEnabled {
		return
	}
	close(c.hcStopCh)
	if c.hcTicker != nil {
		c.hcTicker.Stop()
	}
	c.hcWG.Wait()
	log.Info().Msg("[upstream-cluster] cluster closed")
}

// -----------------------------------------------------------------------------
// Metrics exposition.
// -----------------------------------------------------------------------------

// SnapshotMetrics builds the text exposition of backend_state metrics.
func (c *BackendsCluster) SnapshotMetrics() string {
	var b strings.Builder
	b.WriteString("# HELP backend_state Backend state: 1=present, 0=absent\n")
	b.WriteString("# TYPE backend_state gauge\n")

	c.mu.RLock()
	defer c.mu.RUnlock()

	for name, slot := range c.all {
		for _, st := range []state{healthy, sick, dead} {
			val := 0
			if slot.st == st {
				val = 1
			}
			fmt.Fprintf(&b, "backend_state{backend=\"%s\",state=\"%s\"} %d\n", name, st, val)
		}
	}
	return b.String()
}

// PushMetrics POSTs snapshot to VictoriaMetrics (demo helper).
func (c *BackendsCluster) PushMetrics(client *http.Client, url string) error {
	if client == nil {
		client = http.DefaultClient
	}
	body := strings.NewReader(c.SnapshotMetrics())
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	return nil
}
