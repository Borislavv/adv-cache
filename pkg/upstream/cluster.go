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
	"runtime"
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
	loggerInterval  = 5 * time.Second
	killerInterval  = 24 * time.Hour
	maxThrottles    = 9
	maxCoughsInARow = 5
)

// -----------------------------------------------------------------------------
// Errors.
// -----------------------------------------------------------------------------

var (
	ErrNoBackends                = errors.New("no healthy backends in cluster")
	ErrDuplicate                 = errors.New("backend already exists in cluster")
	ErrNotFound                  = errors.New("backend not found in cluster")
	ErrIsNotDead                 = errors.New("backend is not dead")
	ErrNilBackendConfig          = errors.New("nil backend config")
	ErrAllBackendsAreBusy        = errors.New("all backends are busy")
	ErrAlreadyHasTargetState     = errors.New("backend already has target state")
	ErrMaxThrottleValuesReached  = errors.New("max throttle values reached")
	ErrCannotAcquireIDForBackend = errors.New("cannot acquire ID for backend")
)

// -----------------------------------------------------------------------------
// Aggregate metrics.
// -----------------------------------------------------------------------------

var (
	healthyGauge atomic.Int64
	sickGauge    atomic.Int64
	deadGauge    atomic.Int64
	buriedGauge  atomic.Int64
)

func HealthyBackends() int64 { return healthyGauge.Load() }
func SickBackends() int64    { return sickGauge.Load() }
func DeadBackends() int64    { return deadGauge.Load() }

// -----------------------------------------------------------------------------
// Types & internal State.
// -----------------------------------------------------------------------------

type State uint8

const (
	healthy State = iota
	sick
	dead
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
	backend Backend
	state   State
	rate    atomic.Pointer[rate.Limiter]

	// counters for passive error monitoring
	requestCount atomic.Int64
	errorCount   atomic.Int64
	caughtInRow  atomic.Int64
	throttles    atomic.Int64

	// timestamps
	sickedAt atomic.Int64 // unix nano
	deadAt   atomic.Int64 // unix nano
}

func newBackendSlot(ctx context.Context, backend Backend) *backendSlot {
	rt := backend.Cfg().Rate
	if rt < 1 {
		rt = 1
	}
	//bt := backend.Cfg().Rate / 10
	//if bt < 1 {
	//	bt = 1
	//}
	slot := &backendSlot{
		backend:      backend,
		requestCount: atomic.Int64{},
		errorCount:   atomic.Int64{},
		sickedAt:     atomic.Int64{},
		deadAt:       atomic.Int64{},
		throttles:    atomic.Int64{},
		rate:         atomic.Pointer[rate.Limiter]{},
	}
	slot.rate.Store(rate.NewLimiter(ctx, rt, rt))
	return slot
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

	freeBacksCh <-chan *backendSlot

	hcTicker     <-chan time.Time
	killerTicker <-chan time.Time

	reservedIDs map[string]struct{}
}

// NewCluster initialises cluster & launches active HC.
func NewCluster(ctx context.Context, cfg config.Config) (cluster *BackendCluster, err error) {
	length := len(cfg.Upstream().Cluster.Backends)
	cluster = &BackendCluster{
		ctx:          ctx,
		cfg:          cfg,
		cursor:       atomic.Uint64{},
		healthy:      atomic.Pointer[[]*backendSlot]{},
		all:          make(map[string]*backendSlot, length),
		sick:         make(map[string]*backendSlot, length),
		dead:         make(map[string]*backendSlot, length),
		hcTicker:     utils.NewTicker(ctx, hcInterval),
		killerTicker: utils.NewTicker(ctx, killerInterval),
		reservedIDs:  make(map[string]struct{}, length),
	}

	var healthySlots []*backendSlot
	cluster.healthy.Store(&healthySlots)
	healthyGauge.Store(int64(len(healthySlots)))
	sickGauge.Store(int64(len(cluster.sick)))
	deadGauge.Store(int64(len(cluster.dead)))
	buriedGauge.Store(0)

	gofakeit.Seed(time.Now().UnixNano())
	for _, backendCfg := range cfg.Upstream().Cluster.Backends {
		if err = cluster.Add(backendCfg); err != nil {
			log.Error().Err(err).Msgf("[upstream-cluster] failed adding new backend '%s'", backendCfg.Host)
		}
	}

	if len(*cluster.healthy.Load()) <= 0 {
		log.Info().Msg("[upstream-cluster] cluster initialization failed")
		return nil, ErrNoBackends
	} else {
		log.Info().Msg("[upstream-cluster] cluster initialised")
		cluster.runLogger()
		cluster.runLifecycleManager()
		cluster.freeBacksCh = cluster.provideFreeBackends()
		return cluster, nil
	}
}

// -----------------------------------------------------------------------------
// Hot path (Fetch) – no logs/allocs.
// -----------------------------------------------------------------------------

func (c *BackendCluster) Fetch(rule *config.Rule, ctx *fasthttp.RequestCtx, r *fasthttp.Request) (
	req *fasthttp.Request, resp *fasthttp.Response,
	releaser func(*fasthttp.Request, *fasthttp.Response), err error,
) {
	slot := c.getFreeBackend()
	slot.requestCount.Add(1)
	req, resp, releaser, err = slot.backend.Fetch(rule, ctx, r)
	if err != nil || resp.StatusCode() >= http.StatusInternalServerError {
		slot.errorCount.Add(1)
	}
	return req, resp, releaser, err

	//slots := c.healthy.Load()
	//if len(*slots) == 0 {
	//	return nil, nil, emptyReleaserFn, ErrNoBackends
	//}
	//slotsLen := uint64(len(*slots))
	//
	//for {
	//	// peek next slot by round-robin
	//	slot := (*slots)[int(c.cursor.Add(1)%slotsLen)]
	//
	//	select {
	//	case <-slot.rate.Load().Chan():
	//		slot.requestCount.Add(1)
	//		req, resp, releaser, err = slot.backend.Fetch(rule, ctx, r)
	//		if err != nil || resp.StatusCode() >= http.StatusInternalServerError {
	//			slot.errorCount.Add(1)
	//		}
	//		return req, resp, releaser, err
	//	default:
	//	}
	//}

	//return nil, nil, emptyReleaserFn, ErrAllBackendsAreBusy
}

func (c *BackendCluster) provideFreeBackends() <-chan *backendSlot {
	outCh := make(chan *backendSlot, runtime.GOMAXPROCS(0)*4)
	for _, slot := range *c.healthy.Load() {
		limiterCh := slot.rate.Load().Chan()
		go func(slot *backendSlot, limiterCh <-chan struct{}) {
			for range limiterCh {
				outCh <- slot
			}
		}(slot, limiterCh)
	}
	return outCh
}

func (c *BackendCluster) getFreeBackend() *backendSlot {
	return <-c.freeBacksCh
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
		log.Info().Msgf("[upstream-cluster] adding backend '%s' ignored: duplicate", backend.Name())
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
		logEvent.Msgf("[upstream-cluster] backend '%s' added as healthy (rate=%d, burst=%d)",
			slot.backend.Name(), int(slot.rate.Load().Limit()), slot.rate.Load().Burst())
	} else {
		slot.state = sick
		c.sick[id] = slot
		sickGauge.Add(1)

		log.Info().Msgf("[upstream-cluster] backend '%s' added as sick (rate=%d, burst=%d)",
			slot.backend.Name(), int(slot.rate.Load().Limit()), slot.rate.Load().Burst())
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

	log.Info().Msgf("[upstream-cluster] backend '%s' updated", slot.backend.Name())

	return nil
}

// -----------------------------------------------------------------------------
// Public API (wrappers)
// -----------------------------------------------------------------------------

func (c *BackendCluster) Promote(id string) error    { return c.cure(c.find(id)) }
func (c *BackendCluster) Quarantine(id string) error { return c.quarantine(c.find(id)) }
func (c *BackendCluster) Kill(id string) error       { return c.kill(c.find(id)) }
func (c *BackendCluster) Bury(id string) error       { return c.bury(c.find(id)) }

// -----------------------------------------------------------------------------
// State transitions helpers (with metrics + logs).
// -----------------------------------------------------------------------------

func (c *BackendCluster) find(id string) *backendSlot {
	c.mu.RLock()
	slot := c.all[id]
	c.mu.RUnlock()
	return slot
}

func (c *BackendCluster) cure(slot *backendSlot) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var id = slot.backend.ID()
	var action string
	switch slot.state {
	case healthy:
		return ErrAlreadyHasTargetState
	case sick:
		delete(c.sick, id)
		sickGauge.Add(-1)
		action = "promoted"
	case dead:
		delete(c.dead, id)
		deadGauge.Add(-1)
		action = "resurrected"
	}

	slot.state = healthy
	slot.sickedAt.Store(0)
	slot.deadAt.Store(0)
	slot.caughtInRow.Store(0)
	slot.errorCount.Store(0)
	slot.requestCount.Store(0)
	healthyGauge.Add(1)
	c.appendHealthySlot(slot)

	log.Info().Msgf("[upstream-cluster] backend '%s' %s to healthy", id, action)

	return nil
}

func (c *BackendCluster) resurrect(slot *backendSlot) error {
	return c.cure(slot)
}

func (c *BackendCluster) quarantine(slot *backendSlot) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if slot.state > healthy {
		return ErrAlreadyHasTargetState
	}

	c.removeFromHealthySlice(slot)
	healthyGauge.Add(-1)

	slot.state = sick
	c.sick[slot.backend.ID()] = slot
	sickGauge.Add(1)
	slot.sickedAt.Store(time.Now().UnixNano())

	log.Info().Msgf("[upstream-cluster] backend '%s' quarantined", slot.backend.Name())

	return nil
}

func (c *BackendCluster) kill(slot *backendSlot) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var id = slot.backend.ID()
	switch slot.state {
	case healthy:
		c.removeFromHealthySlice(slot)
		healthyGauge.Add(-1)
	case sick:
		delete(c.sick, id)
		sickGauge.Add(-1)
	case dead:
		return ErrAlreadyHasTargetState
	}

	slot.state = dead
	slot.deadAt.Store(time.Now().UnixNano())
	c.dead[id] = slot
	deadGauge.Add(1)

	log.Info().Msgf("[upstream-cluster] backend '%s' has been killed", slot.backend.Name())

	return nil
}

// Bury permanently removes a dead backend from the cluster and updates metrics.
func (c *BackendCluster) bury(slot *backendSlot) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if slot.state < dead {
		return ErrIsNotDead
	}

	delete(c.dead, slot.backend.ID())
	delete(c.all, slot.backend.ID())

	deadGauge.Add(-1)
	buriedGauge.Add(1)

	log.Info().Msgf("[upstream-cluster] backend '%s' has been buried", slot.backend.Name())

	return nil
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

// appendHealthySlot - renews underlining slice (thread-safe).
func (c *BackendCluster) appendHealthySlot(slot *backendSlot) {
	c.setHealthySlice(append(append([]*backendSlot(nil), *c.healthy.Load()...), slot))
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
// Passive Error Monitor & Quarantine Agent.
// -----------------------------------------------------------------------------

func (c *BackendCluster) runLogger() {
	t := utils.NewTicker(c.ctx, loggerInterval)
	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-t:
				h := healthyGauge.Load()
				s := sickGauge.Load()
				d := deadGauge.Load()
				b := buriedGauge.Load()
				a := h + s + d + b
				log.Info().Msgf("[upstream-healthcheck] backends={all: %d, healthy: %d, sick: %d, dead: %d, buried: %d}", a, h, s, d, b)
			}
		}
	}()
}

// -----------------------------------------------------------------------------
// Active Health Checker.
// -----------------------------------------------------------------------------

func (c *BackendCluster) runLifecycleManager() {
	healthyCheckTicker := utils.NewTicker(c.ctx, time.Second*10)
	sickCheckTicker := utils.NewTicker(c.ctx, time.Second*15)
	tickEach5Min := utils.NewTicker(c.ctx, time.Minute*5)
	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-healthyCheckTicker:
				c.probeHealthyBackends()
			case <-sickCheckTicker:
				c.probeSickBackends()
			case <-tickEach5Min:
				c.probeDeadBackends()
			}
		}
	}()
}

func (c *BackendCluster) probeHealthyBackends() {
	for _, slot := range *c.healthy.Load() {
		go func(slot *backendSlot) {
			name := slot.backend.Name()
			if c.isQuarantineErrorsThresholdWasOvercome(slot) {
				if c.isThrottlePossible(slot) {
					if rt, bt, err := c.throttle(slot); err != nil {
						log.Error().Err(err).Msgf("[upstream-healthcheck] backend '%s' cannot be throttled more (rate: %d, burst: %d)", name, rt, bt)
					} else {
						log.Info().Msgf("[upstream-healthcheck] backend '%s' has been throttled (rate: %d, burst: %d)", name, rt, bt)
					}
				} else {
					if err := c.quarantine(slot); err != nil {
						log.Error().Err(err).Msgf("[upstream-healthcheck] failed to move backend '%s' to quarantined", name)
					} else {
						log.Info().Msgf("[upstream-healthcheck] backend '%s' overcome errors threshold, quarantined", name)
					}
				}
			} else {
				slot.requestCount.Add(1)
				if err := slot.backend.IsHealthy(); err != nil {
					slot.errorCount.Add(1)
					if slot.caughtInRow.Add(1) >= maxCoughsInARow {
						if err = c.quarantine(slot); err != nil {
							log.Error().Err(err).Msgf("[upstream-healthcheck] failed to move backend '%s' to quarantined", name)
						} else {
							log.Info().Msgf("[upstream-healthcheck] backend '%s' caught to many time in a row, quarantined", name)
						}
					} else {
						log.Info().Msgf("[upstream-healthcheck] backend '%s' is coughing: %s", slot.backend.Name(), err.Error())
					}
				} else {
					slot.caughtInRow.Store(0)
				}
			}
		}(slot)
	}
}

func (c *BackendCluster) probeSickBackends() {
	c.mu.RLock()
	now := time.Now().UnixNano()
	for _, slot := range c.sick {
		go func(slot *backendSlot) {
			if err := slot.backend.IsHealthy(); err == nil {
				if err = c.cure(slot); err != nil {
					log.Error().Err(err).Msgf("[upstream-healthcheck] failed to cure backend '%s': %s", slot.backend.Name(), err.Error())
				} else {
					log.Info().Msgf("[upstream-healthcheck] backend '%s' has been cured", slot.backend.Name())
				}
			} else {
				log.Info().Msgf("[upstream-healthcheck] backend '%s' still sick: %s", slot.backend.Name(), err.Error())
			}

			if time.Duration(slot.sickedAt.Load()-now) > time.Hour {
				if err := c.kill(slot); err != nil {
					log.Error().Err(err).Msgf("[upstream-killer] failed to kill backend '%s': %s", slot.backend.Name(), err.Error())
				} else {
					log.Info().Msgf("[upstream-killer] backend '%s' has been killed (downtime=1h)", slot.backend.Name())
				}
			}
		}(slot)
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) probeDeadBackends() {
	c.mu.RLock()
	now := time.Now().UnixNano()
	for _, slot := range c.dead {
		go func(slot *backendSlot) {
			name := slot.backend.Name()

			if err := slot.backend.IsHealthy(); err == nil {
				if err = c.resurrect(slot); err != nil {
					log.Error().Err(err).Msgf("[upstream-healthcheck] failed to resurrect backend '%s': %s", name, err.Error())
				} else {
					log.Error().Msgf("[upstream-healthcheck] backend '%s' has been resurrected", name)
				}
			} else {
				log.Info().Msgf("[upstream-healthcheck] backend '%s' still dead: %s", name, err.Error())
			}

			if time.Duration(slot.sickedAt.Load()-now) > time.Hour*24 {
				if err := c.bury(slot); err != nil {
					log.Error().Err(err).Msgf("[upstream-killer] failed to bury backend '%s': %s", name, err.Error())
				} else {
					log.Info().Msgf("[upstream-killer] backend '%s' has been buried (downtime=24h)", name)
				}
			}
		}(slot)
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) isQuarantineErrorsThresholdWasOvercome(slot *backendSlot) bool {
	r := slot.requestCount.Swap(0)
	e := slot.errorCount.Swap(0)

	if r == 0 {
		return false
	} else if r < 10 {
		return e*70 > r
	} else if r < 100 {
		return e*50 > r
	} else if r < 1000 {
		return e*30 > r
	} else if r < 10000 {
		return e*20 > r
	}
	return e*10 > r
}

func (c *BackendCluster) isThrottlePossible(slot *backendSlot) bool {
	return slot.throttles.Load() <= maxThrottles
}

func (c *BackendCluster) throttle(slot *backendSlot) (r, b int, err error) {
	for {
		old := slot.throttles.Load()
		if old >= maxThrottles {
			oldLimiter := slot.rate.Load()
			return int(oldLimiter.Limit()), slot.rate.Load().Burst(), ErrMaxThrottleValuesReached
		}
		if slot.throttles.CompareAndSwap(old, old+1) {
			or := slot.backend.Cfg().Rate
			rt := or / 10 * ((int(old) + 1) - 10)
			if rt <= 0 {
				rt = 1
			}
			bt := (rt / 30) + 1
			slot.rate.Store(rate.NewLimiter(c.ctx, rt, bt))
			return rt, bt, nil
		}
	}
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

	fmt.Fprintf(&b, "backend_state{backend=\"%s\",state=\"%s\"} %d\n", "...", "buried", buriedGauge.Load())

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
