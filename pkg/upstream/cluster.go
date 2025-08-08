// Package upstream provides a high-performance reverse-proxy cluster
// with a lock-free hot path and copy-on-write state mutations via atomics.
// Active and passive health checks, per-backend rate limiting, and error
// monitoring ensure resilience without contention.
package upstream

import (
	"context"
	"errors"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/brianvoe/gofakeit/v6"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"golang.org/x/time/rate"
)

const (
	hcInterval      = 10 * time.Second
	funeralInterval = 24 * time.Hour
	errorWindow     = 10 * time.Second
)

var (
	ErrNoHealthyBackends = errors.New("no healthy backends in cluster")
	ErrAllBackendsBusy   = errors.New("all backends are busy")
	ErrDuplicateBackend  = errors.New("backend already exists in cluster")
	ErrBackendNotFound   = errors.New("backend not found in cluster")
)

type slotState uint32

const (
	stateInvalid slotState = iota
	stateHealthy
	stateSick
	stateDead
)

func (s slotState) String() string {
	switch s {
	case stateHealthy:
		return "healthy"
	case stateSick:
		return "sick"
	case stateDead:
		return "dead"
	default:
		return "invalid"
	}
}

// backendSlot wraps Backend with state, rate limiter, and error metrics.
type backendSlot struct {
	backend      Backend
	limiter      *rate.Limiter
	state        atomic.Uint32 // slotState
	requestCount atomic.Uint64 // rolling-window requests
	errorCount   atomic.Uint64 // rolling-window errors
	hcRetries    atomic.Uint32 // consecutive health-check failures
}

// NewCluster returns an initialized BackendCluster.
func NewCluster(ctx context.Context, cfg config.Config) (*BackendCluster, error) {
	cluster := &BackendCluster{quarantineCh: make(chan string, 256)}
	// init atomic pointers
	empty := make([]*backendSlot, 0)
	cluster.healthy.Store(&empty)
	cluster.all.Store(&map[string]*backendSlot{})
	cluster.sick.Store(&map[string]*backendSlot{})
	cluster.dead.Store(&map[string]*backendSlot{})

	gofakeit.Seed(time.Now().UnixNano())
	for _, bc := range cfg.Upstream().Cluster.Backends {
		if _, err := cluster.Add(ctx, bc); err != nil {
			log.Error().Err(err).Msgf("[upstream-cluster] failed to add backend %s", bc.Host)
		}
	}
	if len(*cluster.healthy.Load()) == 0 {
		return cluster, ErrNoHealthyBackends
	}

	cluster.runErrorMonitor(ctx)
	cluster.runHealthChecker(ctx)
	cluster.runQuarantineAgent(ctx)
	cluster.runFuneralAgent(ctx)

	log.Info().Msg("[upstream-cluster] cluster started")

	return cluster, nil
}

// BackendCluster manages backends via lock-free snapshots and CAS.
type BackendCluster struct {
	cursor       atomic.Uint64
	healthy      atomic.Pointer[[]*backendSlot]
	all          atomic.Pointer[map[string]*backendSlot]
	sick         atomic.Pointer[map[string]*backendSlot]
	dead         atomic.Pointer[map[string]*backendSlot]
	quarantineCh chan string
}

// Add registers a new backend, unique ID, and initial state.
func (c *BackendCluster) Add(ctx context.Context, bc *config.Backend) (string, error) {
	id := ""
	for i := 0; i < 100_000; i++ {
		cand := strings.ToLower(gofakeit.FirstName())
		old := c.all.Load()
		if _, exists := (*old)[cand]; !exists {
			id = cand
			break
		}
	}
	if id == "" {
		return "", ErrDuplicateBackend
	}
	backend := NewBackend(ctx, bc, id)
	// choose initial state
	st := stateSick
	if err := backend.IsHealthy(); err == nil {
		st = stateHealthy
	}
	slot := newSlot(backend, st)
	// CAS into all map
	c.updateMap(&c.all, id, slot)
	if st == stateHealthy {
		c.updateHealthy(func(h []*backendSlot) []*backendSlot { return append(h, slot) })
	} else {
		c.updateMap(&c.sick, id, slot)
	}
	log.Info().Msgf("[upstream-cluster] added backend '%s' (%s)", backend.Name(), st)
	return id, nil
}

func (c *BackendCluster) Run(ctx context.Context) {
	c.runErrorMonitor(ctx)
	c.runHealthChecker(ctx)
	c.runQuarantineAgent(ctx)
	c.runFuneralAgent(ctx)
}

// Fetch picks a healthy slot lock-free and allocation-free.
func (c *BackendCluster) Fetch(
	rule *config.Rule, ctx *fasthttp.RequestCtx, req *fasthttp.Request,
) (*fasthttp.Request, *fasthttp.Response, func(*fasthttp.Request, *fasthttp.Response), error) {
	slots := *c.healthy.Load()
	n := uint64(len(slots))
	if n == 0 {
		return nil, nil, nil, ErrNoHealthyBackends
	}
	for i := uint64(0); i < n; i++ {
		idx := c.cursor.Add(1)
		s := slots[idx%n]
		if slotState(s.state.Load()) != stateHealthy {
			continue
		}
		s.requestCount.Add(1)
		if !s.limiter.Allow() {
			continue
		}
		r, resp, rel, err := s.backend.Fetch(rule, ctx, req)
		if err != nil || resp.StatusCode() >= 500 {
			s.errorCount.Add(1)
			select {
			case c.quarantineCh <- s.backend.ID():
			default:
			}
		}
		return r, resp, rel, err
	}
	return nil, nil, nil, ErrAllBackendsBusy
}

// Cure moves a backend from sick to healthy.
func (c *BackendCluster) Cure(id string) bool {
	slot := c.getSlot(c.sick.Load(), id)
	if slot == nil || !slot.state.CompareAndSwap(uint32(stateSick), uint32(stateHealthy)) {
		return false
	}
	c.updateMapRemove(&c.sick, id)
	c.updateHealthy(func(h []*backendSlot) []*backendSlot { return append(h, slot) })
	log.Info().Msgf("[upstream-cluster] backend '%s' has been moved to healthy", slot.backend.Name())
	return true
}

// Quarantine moves healthy -> sick.
func (c *BackendCluster) Quarantine(id string) bool {
	slot := c.getSlot(c.all.Load(), id)
	if slot == nil || !slot.state.CompareAndSwap(uint32(stateHealthy), uint32(stateSick)) {
		return false
	}
	c.updateHealthy(func(h []*backendSlot) []*backendSlot { return removeSlot(h, slot) })
	c.updateMap(&c.sick, id, slot)
	log.Info().Msgf("[upstream-cluster] backend '%s' has been moved to quarantine", slot.backend.Name())
	return true
}

// Kill marks a backend dead.
func (c *BackendCluster) Kill(id string) bool {
	slot := c.getSlot(c.all.Load(), id)
	if slot == nil {
		return false
	}
	newState := uint32(stateDead)
	for {
		oldState := slot.state.Load()
		if oldState == newState {
			return false
		}
		if slot.state.CompareAndSwap(oldState, newState) {
			break
		}
	}
	c.updateHealthy(func(h []*backendSlot) []*backendSlot { return removeSlot(h, slot) })
	c.updateMap(&c.dead, id, slot)
	log.Info().Msgf("[upstream-cluster] backend '%s' was killed", id)
	return true
}

// Bury removes backend from all.
func (c *BackendCluster) Bury(id string) bool {
	slot := c.getSlot(c.all.Load(), id)
	c.updateMapRemove(&c.all, id)
	c.updateMapRemove(&c.sick, id)
	c.updateMapRemove(&c.dead, id)
	c.updateHealthy(func(h []*backendSlot) []*backendSlot { return removeSlot(h, slot) })
	log.Info().Msgf("[upstream-cluster] backend '%s' has been buried", slot.backend.Name())
	return true
}

// SnapshotMetrics renders Prometheus metrics.
func (c *BackendCluster) SnapshotMetrics() string {
	var b strings.Builder
	b.WriteString("# HELP backend_state Backend state\n")
	b.WriteString("# TYPE backend_state gauge\n")
	for id, slot := range *c.all.Load() {
		for _, st := range []slotState{stateHealthy, stateSick, stateDead} {
			val := 0
			if slotState(slot.state.Load()) == st {
				val = 1
			}
			b.WriteString(fmt.Sprintf("backend_state{backend=\"%s\",state=\"%s\"} %d\n", id, st, val))
		}
	}
	return b.String()
}

// background routines
func (c *BackendCluster) runErrorMonitor(ctx context.Context) {
	t := utils.NewTicker(ctx, errorWindow)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-t:
				for id, s := range *c.all.Load() {
					r := s.requestCount.Swap(0)
					e := s.errorCount.Swap(0)
					if r > 0 && e*100 > r*10 {
						select {
						case c.quarantineCh <- id:
						default:
						}
						slot := c.getSlot(c.all.Load(), id)
						log.Info().Msgf("[upstream-cluster] backend '%s' overcome errors threshold, quarantined", slot.backend.Name())
					}
				}
			}
		}
	}()
}

func (c *BackendCluster) runQuarantineAgent(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-c.quarantineCh:
				c.Quarantine(id)
			}
		}
	}()
}

func (c *BackendCluster) runHealthChecker(ctx context.Context) {
	t := time.NewTicker(hcInterval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				for id, s := range *c.sick.Load() {
					go func(id string, s *backendSlot) {
						if err := s.backend.IsHealthy(); err == nil {
							c.Cure(id)
						} else if s.hcRetries.Add(1) >= 5 {
							c.Kill(id)
						}
					}(id, s)
				}
			}
		}
	}()
}

func (c *BackendCluster) runLogger(ctx context.Context) {
	t := utils.NewTicker(ctx, 5*time.Second)

	all := len(*c.all.Load())
	healthy := len(*c.healthy.Load())
	sick := len(*c.sick.Load())
	dead := len(*c.dead.Load())

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-t:
				log.Info().Msgf(
					"[upstream-cluster] backends={all: %d, healthy: %d, sick: %d, dead: %d, buried: %d}",
					all, healthy, sick, dead, buried.Load(),
				)
			}
		}
	}()
}

var buried = &atomic.Int32{}

func (c *BackendCluster) runFuneralAgent(ctx context.Context) {
	t := utils.NewTicker(ctx, funeralInterval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-t:
				for id := range *c.dead.Load() {
					name := c.getSlot(c.dead.Load(), id).backend.Name()
					c.Bury(id)
					log.Info().Msgf("[upstream-cluster] backend '%s' has been buried", name)
				}
			}
		}
	}()
}

// helpers
func newSlot(b Backend, st slotState) *backendSlot {
	rateVal := b.Cfg().Rate
	burst := rateVal / 10
	if burst < 1 {
		burst = 1
	}
	s := &backendSlot{backend: b, limiter: rate.NewLimiter(rate.Limit(rateVal), burst)}
	s.state.Store(uint32(st))
	return s
}

func (c *BackendCluster) getSlot(mp *map[string]*backendSlot, id string) *backendSlot {
	return (*mp)[id]
}

func (c *BackendCluster) updateMap(ptr *atomic.Pointer[map[string]*backendSlot],
	id string, slot *backendSlot) {
	for {
		old := ptr.Load()
		newm := cloneMap(*old)
		newm[id] = slot
		if ptr.CompareAndSwap(old, &newm) {
			return
		}
	}
}

func (c *BackendCluster) updateMapRemove(ptr *atomic.Pointer[map[string]*backendSlot], id string) {
	for {
		old := ptr.Load()
		newm := cloneMap(*old)
		delete(newm, id)
		if ptr.CompareAndSwap(old, &newm) {
			return
		}
	}
}

func (c *BackendCluster) updateHealthy(f func([]*backendSlot) []*backendSlot) {
	for {
		old := c.healthy.Load()
		newSlice := f(*old)
		if c.healthy.CompareAndSwap(old, &newSlice) {
			return
		}
	}
}

func cloneMap(src map[string]*backendSlot) map[string]*backendSlot {
	d := make(map[string]*backendSlot, len(src))
	for k, v := range src {
		d[k] = v
	}
	return d
}

func removeSlot(src []*backendSlot, target *backendSlot) []*backendSlot {
	out := make([]*backendSlot, 0, len(src)-1)
	for _, s := range src {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}
