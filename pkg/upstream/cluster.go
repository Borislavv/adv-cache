package upstream

import (
	"context"
	"errors"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"strings"
	"sync/atomic"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"golang.org/x/time/rate"
)

// Errors returned by Fetch when no backends are available or all are busy.
var (
	ErrNotFound                  = errors.New("backend not found in cluster")
	ErrDuplicate                 = errors.New("backend already exists in cluster")
	ErrNoHealthyBackends         = errors.New("no healthy backends in cluster")
	ErrAllBackendsBusy           = errors.New("all backends are busy")
	ErrNilBackendConfig          = errors.New("nil backend config")
	ErrWorkflowStateMismatch     = errors.New("workflow state mismatch")
	ErrCannotAcquireIDForBackend = errors.New("cannot acquire ID for backend")
)

// slotState represents the state of a backend slot.
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

// backendSlot wraps a Backend with state, rate limiting, and counters.
type backendSlot struct {
	backend      Backend
	state        atomic.Uint32 // slotState
	limiter      *rate.Limiter // rate limiter
	requestCount atomic.Uint64 // requests in current window
	errorCount   atomic.Uint64 // errors in current window
	hcRetries    atomic.Uint32 // consecutive health-check failures
}

// newBackendSlot constructs a slot for the given backend and initial state.
func newBackendSlot(b Backend, initial slotState) *backendSlot {
	rt := b.Cfg().Rate
	burst := int(rt / 10)
	if burst < 1 {
		burst = 1
	}
	lim := rate.NewLimiter(rate.Limit(rt), burst)
	s := &backendSlot{backend: b, limiter: lim}
	s.state.Store(uint32(initial))
	return s
}

func (s *backendSlot) State() slotState {
	return slotState(s.state.Load())
}

func (s *backendSlot) TryTransition(from, to slotState) bool {
	return s.state.CompareAndSwap(uint32(from), uint32(to))
}

// BackendCluster manages a pool of backendSlots with lock-free snapshots.
type BackendCluster struct {
	ctx          context.Context
	cfg          config.Config
	cursor       atomic.Uint64                  // round-robin cursor
	healthy      atomic.Pointer[[]*backendSlot] // snapshot of healthy slots
	all          atomic.Pointer[map[string]*backendSlot]
	sick         atomic.Pointer[map[string]*backendSlot]
	dead         atomic.Pointer[map[string]*backendSlot]
	quarantineCh chan string
}

// NewBackendCluster creates and initializes an empty cluster.
func NewBackendCluster(ctx context.Context, cfg config.Config) (*BackendCluster, error) {
	c := &BackendCluster{ctx: ctx, cfg: cfg, quarantineCh: make(chan string, 256)}

	// initialize empty healthy slice
	healthy := make([]*backendSlot, 0)
	c.healthy.Store(&healthy)

	// initialize empty maps
	all := make(map[string]*backendSlot)
	sick := make(map[string]*backendSlot)
	dead := make(map[string]*backendSlot)
	c.all.Store(&all)
	c.sick.Store(&sick)
	c.dead.Store(&dead)

	// create backends
	for _, backCfg := range cfg.Upstream().Cluster.Backends {
		c.Add(backCfg)
	}

	for _, h := range *c.healthy.Load() {
		fmt.Println("healthy: ", h.backend.Name())
	}

	return c, nil
}

// Add registers a new backend, assigns a unique ID, and places it in healthy or sick.
func (c *BackendCluster) Add(cfg *config.Backend) (id string) {
	for {
		id = strings.ToLower(gofakeit.FirstName())
		oldAll := c.all.Load()
		if _, exists := (*oldAll)[id]; exists {
			continue
		}

		b := NewBackend(c.ctx, cfg, id)

		// prepare new map
		newAll := cloneMap(*oldAll)

		// determine initial state via health check
		initial := stateSick
		if err := b.IsHealthy(); err == nil {
			initial = stateHealthy
		}

		// acquire a new slot for backend
		slot := newBackendSlot(b, initial)

		newAll[id] = slot
		if c.all.CompareAndSwap(oldAll, &newAll) {
			// update snapshots
			switch initial {
			case stateHealthy:
				c.updateHealthy(func(h []*backendSlot) []*backendSlot {
					return append(h, slot)
				})
			case stateSick:
				c.updateMap(&c.sick, id, slot)
			}
			log.Info().Msgf("registered backend %s (initial: %s)", id, initial)
			return id
		}
	}
}

// Fetch selects a healthy backend in a lock-free, allocation-free hot path.
// It returns ErrNoHealthyBackends if none healthy, ErrAllBackendsBusy if all rate-limited.
func (c *BackendCluster) Fetch(
	rule *config.Rule,
	ctx *fasthttp.RequestCtx,
	req *fasthttp.Request,
) (
	outReq *fasthttp.Request, outResp *fasthttp.Response,
	releaseFn func(*fasthttp.Request, *fasthttp.Response), err error,
) {
	slots := *c.healthy.Load()
	n := uint64(len(slots))
	if n == 0 {
		panic("really zero")
		err = ErrNoHealthyBackends
		return
	}
	// round-robin tries
	for i := uint64(0); i < n; i++ {
		idx := c.cursor.Add(1)
		s := slots[idx%n]
		if s.State() != stateHealthy {
			continue
		}
		// count and rate-limit
		s.requestCount.Add(1)
		if !s.limiter.Allow() {
			continue
		}
		// perform fetch
		outReq, outResp, releaseFn, err = s.backend.Fetch(rule, ctx, req)
		// track errors
		if err != nil || outResp.StatusCode() >= 500 {
			s.errorCount.Add(1)
			select {
			case c.quarantineCh <- s.backend.ID():
			default:
			}
		}
		return
	}
	// all busy
	err = ErrAllBackendsBusy
	return
}

// Promote moves a sick backend back to healthy.
func (c *BackendCluster) Promote(id string) bool {
	slot := c.getSlot(c.sick.Load(), id)
	if slot == nil || !slot.TryTransition(stateSick, stateHealthy) {
		return false
	}
	c.updateMapRemove(&c.sick, id)
	c.updateHealthy(func(h []*backendSlot) []*backendSlot {
		return append(h, slot)
	})
	log.Info().Msgf("transition sick → healthy for backend %s", id)
	return true
}

// Quarantine demotes a healthy backend to sick.
func (c *BackendCluster) Quarantine(id string) bool {
	slot := c.getSlot(c.all.Load(), id)
	if slot == nil || !slot.TryTransition(stateHealthy, stateSick) {
		return false
	}
	c.updateHealthy(func(h []*backendSlot) []*backendSlot {
		return removeSlot(h, slot)
	})
	c.updateMap(&c.sick, id, slot)
	log.Info().Msgf("transition healthy → sick for backend %s", id)
	return true
}

// Kill marks any backend as dead and removes from healthy.
func (c *BackendCluster) Kill(id string) bool {
	slot := c.getSlot(c.all.Load(), id)
	if slot == nil {
		return false
	}
	// final transition
	for {
		st := slot.State()
		if st == stateDead || slot.TryTransition(st, stateDead) {
			break
		}
	}
	c.updateHealthy(func(h []*backendSlot) []*backendSlot {
		return removeSlot(h, slot)
	})
	c.updateMap(&c.dead, id, slot)
	log.Info().Msgf("transition %s → dead for backend %s", stateDead, id)
	return true
}

// Bury irreversibly removes a backend from all states.
func (c *BackendCluster) Bury(id string) bool {
	slot := c.getSlot(c.all.Load(), id)
	if slot == nil {
		return false
	}
	c.updateMapRemove(&c.all, id)
	c.updateMapRemove(&c.sick, id)
	c.updateMapRemove(&c.dead, id)
	c.updateHealthy(func(h []*backendSlot) []*backendSlot {
		return removeSlot(h, slot)
	})
	log.Info().Msgf("backend %s buried and removed", id)
	return true
}

// SnapshotMetrics returns Prometheus-formatted metrics for backend states.
func (c *BackendCluster) SnapshotMetrics() string {
	var b strings.Builder
	b.WriteString("# HELP backend_state Backend state: 1=present, 0=absent\n")
	b.WriteString("# TYPE backend_state gauge\n")
	allMap := *c.all.Load()
	for id, slot := range allMap {
		for _, st := range []slotState{stateHealthy, stateSick, stateDead} {
			val := 0
			if slot.State() == st {
				val = 1
			}
			b.WriteString(fmt.Sprintf(
				"backend_state{backend=\"%s\",state=\"%s\"} %d\n",
				id, st.String(), val,
			))
		}
	}
	return b.String()
}

// Run starts all background monitoring agents.
func (c *BackendCluster) Run(ctx context.Context) {
	c.runErrorMonitor(ctx)
	c.runQuarantineAgent(ctx)
	c.runHealthChecker(ctx)
	c.runFuneralAgent(ctx)
}

// runErrorMonitor checks error rates every 10s and quarantines if >10%.
func (c *BackendCluster) runErrorMonitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				allMap := *c.all.Load()
				for id, slot := range allMap {
					req := slot.requestCount.Load()
					err := slot.errorCount.Load()
					if req > 0 && err*100 > req*10 {
						select {
						case c.quarantineCh <- id:
						default:
						}
						log.Info().Msgf("error monitor quarantined backend %s", id)
					}
					slot.requestCount.Store(0)
					slot.errorCount.Store(0)
				}
			}
		}
	}()
}

// runQuarantineAgent processes quarantine signals.
func (c *BackendCluster) runQuarantineAgent(ctx context.Context) {
	go func() {
		log.Info().Msg("[quarantine-agent] started")
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-c.quarantineCh:
				c.Quarantine(id)
				log.Info().Msgf("[quarantine-agent] backend %s quarantined", id)
			}
		}
	}()
}

// runHealthChecker probes sick backends every 10s and promotes or kills.
func (c *BackendCluster) runHealthChecker(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		defer ticker.Stop()
		log.Info().Msg("[health-checker] started")
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for id, slot := range *c.sick.Load() {
					go func(id string, s *backendSlot) {
						if err := s.backend.IsHealthy(); err == nil {
							c.Promote(id)
						} else if s.hcRetries.Add(1) >= 3 {
							c.Kill(id)
						}
					}(id, slot)
				}
			}
		}
	}()
}

// runFuneralAgent removes dead backends every 24h.
func (c *BackendCluster) runFuneralAgent(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		defer ticker.Stop()
		log.Info().Msg("[funeral-agent] started")
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count := 0
				for id := range *c.dead.Load() {
					if c.Bury(id) {
						count++
					}
				}
				log.Info().Msgf("[funeral-agent] buried %d dead backends", count)
			}
		}
	}()
}

// getSlot retrieves a slot from a map pointer.
func (c *BackendCluster) getSlot(mpPtr *map[string]*backendSlot, id string) *backendSlot {
	return (*mpPtr)[id]
}

// updateMap adds or updates a slot in the given atomic map pointer.
func (c *BackendCluster) updateMap(
	ptr *atomic.Pointer[map[string]*backendSlot],
	id string,
	slot *backendSlot,
) bool {
	for {
		old := ptr.Load()
		newMap := cloneMap(*old)
		newMap[id] = slot
		if ptr.CompareAndSwap(old, &newMap) {
			return true
		}
	}
}

// updateMapRemove removes a slot from the given atomic map pointer.
func (c *BackendCluster) updateMapRemove(
	ptr *atomic.Pointer[map[string]*backendSlot],
	id string,
) bool {
	for {
		old := ptr.Load()
		newMap := cloneMap(*old)
		delete(newMap, id)
		if ptr.CompareAndSwap(old, &newMap) {
			return true
		}
	}
}

// updateHealthy atomically transforms the healthy slice.
func (c *BackendCluster) updateHealthy(
	transform func([]*backendSlot) []*backendSlot,
) {
	for {
		old := c.healthy.Load()
		newSlice := transform(*old)
		if c.healthy.CompareAndSwap(old, &newSlice) {
			return
		}
	}
}

// cloneMap shallow-copies a map of backend slots.
func cloneMap(src map[string]*backendSlot) map[string]*backendSlot {
	d := make(map[string]*backendSlot, len(src))
	for k, v := range src {
		d[k] = v
	}
	return d
}

// removeSlot filters out a slot from a slice.
func removeSlot(src []*backendSlot, target *backendSlot) []*backendSlot {
	out := make([]*backendSlot, 0, len(src)-1)
	for _, s := range src {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}
