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
// * per‑backend gauge  `backend_state{backend="id",State="healthy|sick|dead"}`
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
	hcInterval      = 10 * time.Second
	killerInterval  = 24 * time.Hour
	errorWindow     = 1 * time.Minute // sliding window for error monitoring
	funeralInterval = 24 * time.Hour  // how often to bury dead backends
)

// -----------------------------------------------------------------------------
// Errors.
// -----------------------------------------------------------------------------

var (
	ErrNoBackends                = errors.New("no healthy backends in cluster")
	ErrDuplicate                 = errors.New("backend already exists in cluster")
	ErrNotFound                  = errors.New("backend not found in cluster")
	ErrNilBackendConfig          = errors.New("nil backend config")
	ErrAllBackendsAreBusy        = errors.New("all backends are busy")
	ErrWorkflowStateMismatch     = errors.New("workflow state mismatch")
	ErrCannotAcquireIDForBackend = errors.New("cannot acquire ID for backend")
)

// -----------------------------------------------------------------------------
// Aggregate metrics.
// -----------------------------------------------------------------------------

var (
	healthyGauge   atomic.Int64
	sickGauge      atomic.Int64
	deadGauge      atomic.Int64
	buriedGauge    atomic.Int64
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

	// counters for passive error monitoring
	requestCount atomic.Int64
	errorCount   atomic.Int64
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
		backend:      backend,
		hcRetries:    atomic.Int32{},
		requestCount: atomic.Int64{},
		errorCount:   atomic.Int64{},
		rate:         *rate.NewLimiter(ctx, rt, bt),
	}
}

// -----------------------------------------------------------------------------
// Cluster.
// -----------------------------------------------------------------------------

type BackendCluster struct {
	ctx context.Context
	cfg config.Config

	cursor  atomic.Uint64                  // RR cursor (overflow‑safe)
	healthy atomic.Pointer[[]*backendSlot] // immutable healthy slice

	mu   sync.RWMutex            // guards maps & slice rebuilds
	all  map[string]*backendSlot // every backend by id
	sick map[string]*backendSlot // quarantined
	dead map[string]*backendSlot // removed

	quarantineCh     chan string
	quarantineTicker <-chan time.Time

	hcTicker     <-chan time.Time
	killerTicker <-chan time.Time

	reservedIDs map[string]struct{}
}

// NewCluster initialises cluster & launches active HC.
func NewCluster(ctx context.Context, cfg config.Config) (cluster *BackendCluster, err error) {
	length := len(cfg.Upstream().Cluster.Backends)
	cluster = &BackendCluster{
		ctx:              ctx,
		cfg:              cfg,
		cursor:           atomic.Uint64{},
		healthy:          atomic.Pointer[[]*backendSlot]{},
		all:              make(map[string]*backendSlot, length),
		sick:             make(map[string]*backendSlot, length),
		dead:             make(map[string]*backendSlot, length),
		hcTicker:         utils.NewTicker(ctx, hcInterval),
		killerTicker:     utils.NewTicker(ctx, killerInterval),
		reservedIDs:      make(map[string]struct{}, length),
		quarantineCh:     make(chan string, length*4+1),
		quarantineTicker: utils.NewTicker(ctx, killerInterval),
	}

	var healthySlots []*backendSlot
	cluster.healthy.Store(&healthySlots)
	healthyGauge.Store(int64(len(healthySlots)))
	sickGauge.Store(int64(len(cluster.sick)))

	gofakeit.Seed(time.Now().UnixNano())
	for _, backendCfg := range cfg.Upstream().Cluster.Backends {
		if err = cluster.Add(backendCfg); err != nil {
			log.Error().Err(err).Msg("[upstream-cluster] adding backend failed")
		}
	}

	if len(healthySlots) <= 0 {
		logEvent := log.Error().Err(ErrNoBackends)
		if cfg.IsProd() {
			logEvent.
				Int64("healthy", HealthyBackends()).
				Int64("sick", SickBackends()).
				Int64("dead", DeadBackends())
		}
		logEvent.Msg("[upstream-cluster] cluster initialization failed")
		return cluster, ErrNoBackends
	} else {
		logEvent := log.Info()
		if cfg.IsProd() {
			logEvent.
				Int64("healthy", HealthyBackends()).
				Int64("sick", SickBackends()).
				Int64("dead", DeadBackends())
		}
		logEvent.Msg("[upstream-cluster] cluster initialised")
		return cluster, nil
	}
}

func (c *BackendCluster) Run() {
	c.runErrorMonitor()
	c.runHealthChecker()
	c.runQuarantineAgent()
	c.runFuneralAgent()
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
	for scans := uint64(0); scans < slotsLen; scans++ {
		// peek next slot by round-robin
		slot := (*slots)[int(c.cursor.Add(1)%slotsLen)]
		// check whether it can handle else one request
		select {
		case <-slot.rate.Chan():
			// if so, do request
			req, resp, releaser, err = slot.backend.Fetch(rule, ctx, r)
			if err != nil || resp.StatusCode() >= http.StatusInternalServerError {
				c.markSickAsync(slot.backend.ID())
			}
			return req, resp, releaser, err
		default:
		}
	}

	return nil, nil, emptyReleaserFn, ErrAllBackendsAreBusy
}

// -----------------------------------------------------------------------------
// Mutation API.
// -----------------------------------------------------------------------------

func (c *BackendCluster) Add(cfg *config.Backend) error {
	if cfg == nil {
		return ErrNilBackendConfig
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var id string
	for range 100_000 {
		candID := strings.ToLower(gofakeit.FirstName())
		if _, dup := c.reservedIDs[candID]; !dup {
			id = candID
			c.reservedIDs[id] = struct{}{}
			break
		}
	}
	if id == "" {
		return ErrCannotAcquireIDForBackend
	}

	backend := NewBackend(c.ctx, cfg, id)

	if _, dup := c.all[id]; dup {
		logEvent := log.Info()
		if c.cfg.IsProd() {
			logEvent.
				Str("from", "new")
		}
		logEvent.Msgf("[upstream-cluster] adding backend '%s' ignored: duplicate", backend.Name())
		return ErrDuplicate
	}

	slot := newBackendSlot(c.ctx, backend)
	c.all[id] = slot

	if err := backend.IsHealthy(); err == nil {
		slot.state = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)

		logEvent := log.Info()
		if c.cfg.IsProd() {
			logEvent.
				Str("from", "new").
				Str("to", slot.state.String()).
				Str("backend", slot.backend.Name())
		}
		logEvent.Msgf("[upstream-cluster] backend '%s' added as healthy", slot.backend.Name())
	} else {
		slot.state = sick
		c.sick[id] = slot
		sickGauge.Add(1)

		logEvent := log.Info()
		if c.cfg.IsProd() {
			logEvent.
				Str("from", "new").
				Str("to", slot.state.String()).
				Str("backend", slot.state.String())
		}
		logEvent.Msgf("[upstream-cluster] backend '%s' added as sick", slot.backend.Name())
	}

	return nil
}

func (c *BackendCluster) Update(ctx context.Context, id string, cfg *config.Backend) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[id]
	if !ok {
		return ErrNotFound
	}

	var be Backend
	if cfg != nil {
		be = NewBackend(ctx, cfg, id)
	} else {
		be = slot.backend
	}

	prev := slot.state
	if prev == healthy {
		c.removeFromHealthySlice(slot)
		healthyGauge.Add(-1)
	} else if prev == sick {
		delete(c.sick, id)
		sickGauge.Add(-1)
	}

	slot.backend = be
	if err := be.IsHealthy(); err == nil {
		slot.state = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)
	} else {
		slot.state = sick
		c.sick[id] = slot
		sickGauge.Add(1)
	}

	logEvent := log.Info()
	if c.cfg.IsProd() {
		logEvent.
			Str("from", prev.String()).
			Str("to", slot.state.String()).
			Str("backend", slot.backend.Name())
	}
	logEvent.Msgf("[upstream-cluster] backend '%s' updated", slot.backend.Name())

	return nil
}

func (c *BackendCluster) Resurrect(ctx context.Context, id string, cfg *config.Backend) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.dead[id]
	if !ok {
		return ErrNotFound
	}
	delete(c.dead, id)
	deadGauge.Add(-1)

	if cfg != nil {
		slot.backend = NewBackend(ctx, cfg, id)
	}

	if err := slot.backend.IsHealthy(); err == nil {
		slot.state = healthy
		c.appendHealthySlot(slot)
		healthyGauge.Add(1)
	} else {
		slot.state = sick
		c.sick[id] = slot
		sickGauge.Add(1)
	}

	c.all[id] = slot

	logEvent := log.Info()
	if c.cfg.IsProd() {
		logEvent.
			Str("from", dead.String()).
			Str("to", slot.state.String()).
			Str("backend", slot.backend.Name())
	}
	logEvent.Msgf("[upstream-cluster] backend '%s' resurrected", slot.backend.Name())

	return nil
}

// -----------------------------------------------------------------------------
// Public API (wrappers)
// -----------------------------------------------------------------------------

func (c *BackendCluster) Promote(id string) error    { return c.promote(id) }
func (c *BackendCluster) Quarantine(id string) error { return c.quarantine(id) }
func (c *BackendCluster) Kill(id string) error       { return c.kill(id) }
func (c *BackendCluster) Bury(id string) error       { return c.bury(id) }

// -----------------------------------------------------------------------------
// State transitions helpers (with metrics + logs).
// -----------------------------------------------------------------------------

// Bury permanently removes a dead backend from the cluster and updates metrics.
func (c *BackendCluster) bury(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.dead[id]; !ok {
		return ErrNotFound
	}

	delete(c.dead, id)
	delete(c.all, id)

	deadGauge.Add(-1)
	buriedGauge.Add(1)

	return nil
}

func (c *BackendCluster) quarantine(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[id]
	if !ok {
		return ErrNotFound
	}
	if slot.state != healthy {
		return ErrWorkflowStateMismatch
	}

	c.removeFromHealthySlice(slot)
	healthyGauge.Add(-1)

	slot.state = sick
	c.sick[id] = slot
	sickGauge.Add(1)

	log.Info().Msgf("[upstream-cluster] backend '%s' quarantined", slot.backend.Name())

	return nil
}

func (c *BackendCluster) promote(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.sick[id]
	if !ok {
		return ErrNotFound
	}
	delete(c.sick, id)
	sickGauge.Add(-1)

	slot.state = healthy
	c.appendHealthySlot(slot)
	healthyGauge.Add(1)

	logEvent := log.Info()
	if c.cfg.IsProd() {
		logEvent.
			Str("backend", id)
	}
	logEvent.Msgf("[upstream-cluster] backend '%s' promoted to healthy", id)

	return nil
}

func (c *BackendCluster) kill(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[id]
	if !ok {
		return ErrNotFound
	}

	switch slot.state {
	case healthy:
		c.removeFromHealthySlice(slot)
		healthyGauge.Add(-1)
	case sick:
		delete(c.sick, id)
		sickGauge.Add(-1)
	}

	slot.state = dead
	c.dead[id] = slot
	deadGauge.Add(1)

	log.Info().Msgf("[upstream-cluster] backend '%s' buried", slot.backend.Name())

	return nil
}

func (c *BackendCluster) markSickAsync(id string) {
	select {
	case c.quarantineCh <- id: // move to quarantine on hit
	default: // do nothing, it's hot path, was just lost one not so important action
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

// -----------------------------------------------------------------------------
// Passive Error Monitor & Quarantine Agent.
// -----------------------------------------------------------------------------

func (c *BackendCluster) runErrorMonitor() {
	t := utils.NewTicker(c.ctx, errorWindow)
	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-t:
				for id, slot := range c.all {
					// swap counts
					r := slot.requestCount.Swap(0)
					e := slot.errorCount.Swap(0)
					if r > 0 && e*100 > r*10 {
						select {
						case c.quarantineCh <- id:
						default:
						}
						s := slot.backend.Name()
						log.Info().Msgf("[upstream-cluster] backend '%s' exceeded error threshold, quarantined", s)
					}
				}
			}
		}
	}()
}

func (c *BackendCluster) runQuarantineAgent() {
	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case id := <-c.quarantineCh:
				_ = c.Quarantine(id)
			}
		}
	}()
}

// -----------------------------------------------------------------------------
// Active Health Checker.
// -----------------------------------------------------------------------------

func (c *BackendCluster) runHealthChecker() {
	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-c.hcTicker:
				for id, slot := range c.sick {
					go func(id string, s *backendSlot) {
						all, healthy, sick, dead, buried := c.probeSickBackends()
						log.Info().Msgf("[upstream-cluster] backends={all: %d, healthy: %d, sick: %d, dead: %d, buried: %d}",
							all, healthy, sick, dead, buried)
					}(id, slot)
				}
			}
		}
	}()
}

func (c *BackendCluster) probeSickBackends() (all, healthy, sick, dead, buried int) {
	wg := sync.WaitGroup{}

	c.mu.RLock()
	for _, slot := range c.sick {
		wg.Add(1)
		go func(slot *backendSlot) {
			defer wg.Done()

			if err := slot.backend.IsHealthy(); err == nil {
				if err = c.promote(slot.backend.ID()); err != nil {
					log.Error().Msgf("[upstream-cluster] failed to promote backend '%s' to healthy: %s", slot.backend.Name(), err.Error())
				}
			} else {
				log.Info().Msgf("[upstream-cluster] backend '%s' still sick: %s", slot.backend.Name(), err.Error())
			}

			const maxRetries = 360
			if slot.hcRetries.Add(1) >= maxRetries {
				if err := c.kill(slot.backend.ID()); err != nil {
					log.Info().Msgf("[upstream-cluster] failed to kill backend '%s': %s", slot.backend.Name(), err.Error())
				}
			}
		}(slot)
	}
	c.mu.RUnlock()

	wg.Wait()
	c.mu.RLock()
	allLen := len(c.all)
	sickLen := len(c.sick)
	deadLen := len(c.dead)
	c.mu.RUnlock()

	return allLen, len(*c.healthy.Load()), sickLen, deadLen, int(buriedGauge.Load())
}

// -----------------------------------------------------------------------------
// Funeral Agent (buries dead backends).
// -----------------------------------------------------------------------------

func (c *BackendCluster) runFuneralAgent() {
	t := utils.NewTicker(c.ctx, funeralInterval)
	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-t:
				for id := range c.dead {
					name := c.all[id].backend.Name()
					if err := c.bury(id); err == nil {
						buriedGauge.Add(1)
						log.Info().Msgf("[upstream-cluster] backend '%s' has been buried", name)
					}
				}
			}
		}
	}()
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
