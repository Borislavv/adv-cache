// Package upstream provides a minimal reverse‑proxy cluster with passive and
// optional active health checks. The hot path is lock‑free and allocation‑free;
// all mutations use copy‑on‑write under a coarse RW lock.
//
// Logging
// -------
// All non‑hot‑path State transitions are traced with zerolog using the common
// prefix **[upstream‑cluster]**.  Это даёт чёткую цепочку действий без шума в
// критическом трафике.
//
// Metrics
// -------
// * aggregate gauges: Healthy / Sick / Dead
// * per‑backend gauge  `backend_state{backend="name",State="healthy|sick|dead"}`
//
// Helper `SnapshotMetrics()` возвращает payload в формате Prometheus, готовый
// к push в VictoriaMetrics (`/api/v1/import/prometheus`) или scrape любым
// Prometheus‑совместимым сборщиком.
package upstream

import (
	"context"
	"errors"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/rate"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/valyala/fasthttp"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/rs/zerolog/log"

	"github.com/Borislavv/advanced-cache/pkg/config"
)

const (
	hcInterval     = time.Second * 10
	killerInterval = time.Hour * 24
)

// -----------------------------------------------------------------------------
// Errors.
// -----------------------------------------------------------------------------

var (
	ErrNoBackends         = errors.New("no healthy backends in cluster")
	ErrDuplicate          = errors.New("backend already exists in cluster")
	ErrNotFound           = errors.New("backend not found in cluster")
	ErrNilBackend         = errors.New("nil backend")
	ErrAllBackendsAreBusy = errors.New("all backends are busy")
)

// -----------------------------------------------------------------------------
// Aggregate metrics.
// -----------------------------------------------------------------------------

var (
	healthyGauge   atomic.Int64
	sickGauge      atomic.Int64
	deadGauge      atomic.Int64
	forgottenGauge atomic.Int64
)

func HealthyBackends() int64 { return healthyGauge.Load() }
func SickBackends() int64    { return sickGauge.Load() }
func DeadBackends() int64    { return deadGauge.Load() }

// -----------------------------------------------------------------------------
// Types & internal State.
// -----------------------------------------------------------------------------

type State uint8

const (
	_ State = iota
	healthy
	sick
	dead
	forgotten
)

func (s State) String() string {
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
	backend   Backend
	state     State
	hcRetries atomic.Int32
	rate      rate.Limiter
}

func newBackendSlot(ctx context.Context, backend Backend) *backendSlot {
	rt := backend.Cfg().Rate
	if rt < 1 {
		rt = 1
	}
	bt := backend.Cfg().Rate / 10
	if bt < 1 {
		bt = 1
	}
	return &backendSlot{
		backend:   backend,
		hcRetries: atomic.Int32{},
		rate:      *rate.NewLimiter(ctx, rt, bt),
	}
}

// -----------------------------------------------------------------------------
// Cluster.
// -----------------------------------------------------------------------------

type BackendCluster struct {
	ctx context.Context
	cfg config.Config

	mu   sync.RWMutex            // guards maps & slice rebuilds
	all  map[string]*backendSlot // every backend by name
	sick map[string]*backendSlot // quarantined
	dead map[string]*backendSlot // removed

	cursor  atomic.Uint64                  // RR cursor (overflow‑safe)
	healthy atomic.Pointer[[]*backendSlot] // immutable healthy slice

	hcTicker     <-chan time.Time
	killerTicker <-chan time.Time
}

// NewCluster initialises cluster & launches active HC.
func NewCluster(ctx context.Context, cfg config.Config) (c *BackendCluster, err error) {
	c = &BackendCluster{
		ctx:          ctx,
		cfg:          cfg,
		all:          make(map[string]*backendSlot),
		sick:         make(map[string]*backendSlot),
		dead:         make(map[string]*backendSlot),
		hcTicker:     utils.NewTicker(ctx, hcInterval),
		killerTicker: utils.NewTicker(ctx, killerInterval),
		names:        make(map[string]struct{}, len(cfg.Upstream().Cluster.Backends)),
	}

	gofakeit.Seed(time.Now().UnixNano())
	reserved := make(map[string]struct{}, len(cfg.Upstream().Cluster.Backends))

	var healthySlots []*backendSlot
	for _, bcfg := range cfg.Upstream().Cluster.Backends {
		name := bcfg.Name
		if name == "" {
			for i := 0; i < 10_000; i++ {
				cand := strings.ToLower(gofakeit.FirstName())
				if _, dup := reserved[cand]; !dup {
					name = cand
					reserved[name] = struct{}{}
					break
				}
			}
		}

		slot := newBackendSlot(ctx, NewBackend(ctx, bcfg, name))
		if slot.backend.IsHealthy() {
			slot.state = healthy
			healthySlots = append(healthySlots, slot)
		} else {
			slot.state = sick
			c.sick[name] = slot
		}
		c.all[name] = slot
	}

	c.setHealthySlice(healthySlots)
	healthyGauge.Store(int64(len(healthySlots)))
	sickGauge.Store(int64(len(c.sick)))

	if len(healthySlots) == 0 {
		err = ErrNoBackends
		log.Error().
			Int64("healthy", HealthyBackends()).
			Int64("sick", SickBackends()).
			Int64("dead", DeadBackends()).
			Msg("[upstream-cluster] init failed: no healthy backends")
	} else {
		log.Info().
			Int64("healthy", HealthyBackends()).
			Int64("sick", SickBackends()).
			Int64("dead", DeadBackends()).
			Msg("[upstream-cluster] cluster initialised")
	}

	// start a lifecycle manager
	c.runLifeCycle()

	return
}

// -----------------------------------------------------------------------------
// Hot path (Fetch) – no logs/allocs.
// -----------------------------------------------------------------------------

func (c *BackendCluster) Fetch(rule *config.Rule, ctx *fasthttp.RequestCtx, r *fasthttp.Request) (
	req *fasthttp.Request, resp *fasthttp.Response,
	releaser func(*fasthttp.Request, *fasthttp.Response), err error,
) {
	slots := c.healthy.Load()
	if len(*slots) == 0 {
		return nil, nil, emptyReleaserFn, ErrNoBackends
	}
	slotsLen := uint64(len(*slots))

	// attempts number = healthy backends number
	for _i := uint64(0); _i < slotsLen; _i++ {
		// peek next slot by round-robin
		slot := (*slots)[int(c.cursor.Add(1)%slotsLen)]
		// check whether it can handle else one request
		select {
		case <-c.ctx.Done():
			return nil, nil, emptyReleaserFn, c.ctx.Err()
		case <-slot.rate.Chan():
			// if so, do request
			req, resp, releaser, err = slot.backend.Fetch(rule, ctx, r)
			if err != nil || resp.StatusCode() >= http.StatusInternalServerError {
				go c.markSickAsync(slot.backend.Name())
			}
			return req, resp, releaser, err
		default:
			continue
		}
	}

	return nil, nil, emptyReleaserFn, ErrAllBackendsAreBusy
}

// -----------------------------------------------------------------------------
// Mutation API.
// -----------------------------------------------------------------------------

func (c *BackendCluster) Add(backend Backend) error {
	if backend == nil {
		return ErrNilBackend
	}
	name := backend.Name()
	if name == "" {
		for i := 0; i < 10_000; i++ {
			cand := strings.ToLower(gofakeit.FirstName())
			if _, dup := reserved[cand]; !dup {
				name = cand
				reserved[name] = struct{}{}
				break
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, dup := c.all[name]; dup {
		log.Warn().Str("backend", name).Msg("[upstream-cluster] add ignored: duplicate")
		return ErrDuplicate
	}

	slot := newBackendSlot(c.ctx, backend)
	c.all[name] = slot

	if backend.IsHealthy() {
		slot.state = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)
		log.Info().Str("backend", name).Msg("[upstream-cluster] backend added as healthy")
	} else {
		slot.state = sick
		c.sick[name] = slot
		sickGauge.Add(1)
		log.Info().Str("backend", name).Msg("[upstream-cluster] backend added as sick")
	}
	return nil
}

func (c *BackendCluster) Update(ctx context.Context, name string, cfg *config.Backend) error {
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

	prev := slot.state
	if prev == healthy {
		c.removeFromHealthySlice(slot)
		healthyGauge.Add(-1)
	} else if prev == sick {
		delete(c.sick, name)
		sickGauge.Add(-1)
	}

	slot.backend = be
	if be.IsHealthy() {
		slot.state = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)
	} else {
		slot.state = sick
		c.sick[name] = slot
		sickGauge.Add(1)
	}

	log.Info().Str("backend", name).Str("from", prev.String()).Str("to", slot.state.String()).
		Msg("[upstream-cluster] backend updated")
	return nil
}

func (c *BackendCluster) Resurrect(ctx context.Context, name string, cfg *config.Backend) error {
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
		slot.state = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)
	} else {
		slot.state = sick
		c.sick[name] = slot
		sickGauge.Add(1)
	}

	c.all[name] = slot
	log.Info().Str("backend", name).Msg("[upstream-cluster] backend resurrected")
	return nil
}

// wrappers
func (c *BackendCluster) Promote(name string) error    { return c.promote(name) }
func (c *BackendCluster) Quarantine(name string) error { return c.quarantine(name) }
func (c *BackendCluster) Kill(name string) error       { return c.kill(name) }

// -----------------------------------------------------------------------------
// State transitions helpers (with metrics + logs).
// -----------------------------------------------------------------------------

func (c *BackendCluster) quarantine(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[name]
	if !ok {
		return ErrNotFound
	}
	if slot.state != healthy {
		return nil
	}

	c.removeFromHealthySlice(slot)
	healthyGauge.Add(-1)

	slot.state = sick
	c.sick[name] = slot
	sickGauge.Add(1)

	log.Info().Str("backend", name).Msg("[upstream-cluster] backend quarantined")
	return nil
}

func (c *BackendCluster) promote(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.sick[name]
	if !ok {
		return ErrNotFound
	}
	delete(c.sick, name)
	sickGauge.Add(-1)

	slot.state = healthy
	c.appendHealthySlot(slot)
	healthyGauge.Add(1)

	log.Info().Str("backend", name).Msg("[upstream-cluster] backend promoted to healthy")
	return nil
}

func (c *BackendCluster) kill(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[name]
	if !ok {
		return ErrNotFound
	}

	switch slot.state {
	case healthy:
		c.removeFromHealthySlice(slot)
		healthyGauge.Add(-1)
	case sick:
		delete(c.sick, name)
		sickGauge.Add(-1)
	}

	slot.state = dead
	c.dead[name] = slot
	deadGauge.Add(1)

	log.Info().Str("backend", name).Msg("[upstream-cluster] backend buried (dead)")
	return nil
}

func (c *BackendCluster) markSickAsync(name string) {
	if err := c.quarantine(name); err == nil {
		log.Warn().Str("backend", name).Msg("[upstream-cluster] backend quarantined due to error")
	}
}

// -----------------------------------------------------------------------------
// Slice helpers (no logging).
// -----------------------------------------------------------------------------

func (c *BackendCluster) setHealthySlice(slots []*backendSlot) {
	c.healthy.Store(&slots)
	if len(slots) == 0 {
		c.cursor.Store(0)
	}
}

func (c *BackendCluster) appendHealthySlot(slot *backendSlot) {
	old := c.healthy.Load()
	newSlice := append(append([]*backendSlot(nil), *old...), slot)
	c.setHealthySlice(newSlice)
}

func (c *BackendCluster) removeFromHealthySlice(slot *backendSlot) {
	old := c.healthy.Load()
	newSlice := make([]*backendSlot, 0, len(*old)-1)
	for _, s := range *old {
		if s != slot {
			newSlice = append(newSlice, s)
		}
	}
	c.setHealthySlice(newSlice)
}

// -----------------------------------------------------------------------------
// LifeCycle checkers.
// -----------------------------------------------------------------------------

func (c *BackendCluster) runLifeCycle() {
	c.runKiller()
	c.runHealthChecker()
}

func (c *BackendCluster) runKiller() {
	log.Info().Dur("hcInterval", killerInterval).Msg("[upstream-cluster] killer started")

	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-c.killerTicker:
				c.killDoomedBackends()
			}
		}
	}()
}

const maxRetries = 6

func (c *BackendCluster) runHealthChecker() {
	log.Info().Dur("hcInterval", hcInterval).Msg("[upstream-cluster] active HC started")

	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-c.hcTicker:
				c.probeSickBackends()
			}
		}
	}()
}

func (c *BackendCluster) probeSickBackends() {
	c.mu.RLock()
	for _, slot := range c.sick {
		go func(slot *backendSlot) {
			if slot.backend.IsHealthy() {
				if err := c.promote(slot.backend.Name()); err != nil {
					log.Error().Err(err).Msgf("[upstream-cluster] failed to promote backend '%s' to healthy", slot.backend.Name())
				} else {
					log.Info().Msgf("[upstream-cluster] backend '%s' promoted to healthy", slot.backend.Name())
					return
				}
			}

			if slot.hcRetries.Add(1) >= maxRetries {
				if err := c.kill(slot.backend.Name()); err != nil {
					log.Info().Msgf("[upstream-cluster] failed to kill backend '%s'", slot.backend.Name())
				}
			}
		}(slot)
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) killDoomedBackends() {
	forgottenGauge.Add(int64(len(c.dead)))

	c.mu.Lock()
	for _, slot := range c.dead {
		delete(c.all, slot.backend.Name())
		delete(c.dead, slot.backend.Name())
	}
	c.mu.Unlock()
}

// -----------------------------------------------------------------------------
// Metrics exposition.
// -----------------------------------------------------------------------------

// SnapshotMetrics builds the text exposition of backend_state metrics.
func (c *BackendCluster) SnapshotMetrics() string {
	var b strings.Builder
	b.WriteString("# HELP backend_state Backend State: 1=present, 0=absent\n")
	b.WriteString("# TYPE backend_state gauge\n")

	c.mu.RLock()
	defer c.mu.RUnlock()

	for name, slot := range c.all {
		for _, st := range []State{healthy, sick, dead} {
			val := 0
			if slot.state == st {
				val = 1
			}
			fmt.Fprintf(&b, "backend_state{backend=\"%s\",state=\"%s\"} %d\n", name, st, val)
		}
	}

	fmt.Fprintf(&b, "backend_state{backend=\"%s\",state=\"%s\"} %d\n", "...", forgotten, forgottenGauge.Load())

	return b.String()
}

// PushMetrics POSTs snapshot to VictoriaMetrics (demo helper).
func (c *BackendCluster) PushMetrics(client *http.Client, url string) error {
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
