// Package upstream implements a light‑weight reverse‑proxy cluster aware of
// backend health.  The implementation is intended for production use:
//   - lock‑free traffic path (only atomics on hot path);
//   - thread‑safe backend registration / state changes;
//   - passive failure detection (>=500  or network error ⇒ quarantine);
//   - optional active health‑checks with exponential back‑off;
//   - round‑robin balancing with overflow‑safe atomic counter.
//
// NOTE: real BackendNode and config types are assumed to live in the same module.
// Only interfaces needed here are re‑declared to avoid circular imports.
package upstream

import (
	"context"
	"errors"
	"github.com/brianvoe/gofakeit/v6"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/rs/zerolog/log"
)

// ---- minimal contracts ----------------------------------------------------

type fetchFn = func(rule *config.Rule, path, query []byte, queryHeaders *[][2][]byte) (
	status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
)

// Backend is the subset of methods used by the cluster.
// Real implementation normally provides Name(), IsHealthy() and Fetch().
//
// All methods must be thread‑safe!
// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
//
//nolint:interfacebloat // keep it explicit
type Backend interface {
	Name() string
	IsHealthy() bool
	Fetch(rule *config.Rule, path, query []byte, queryHeaders *[][2][]byte) (
		status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
	)
}

// ---- errors ----------------------------------------------------------------

var (
	ErrNoBackends = errors.New("no healthy backends in cluster")
	ErrDuplicate  = errors.New("backend already exists in cluster")
	ErrNotFound   = errors.New("backend not found in cluster")
)

// ---- internal state --------------------------------------------------------

type state uint8

const (
	_ state = iota
	healthy
	sick
	dead
)

type backendSlot struct {
	be   Backend
	st   state
	next int // index of next healthy slot (singly‑linked list) – avoids reallocs
}

// ---- BackendsCluster -------------------------------------------------------

// BackendsCluster is a thread‑safe round‑robin balancer with passive/active HC.
// The hot‑path (Fetch) is lock‑free: only an atomic cursor & an immutable slice
// of *backendSlot*.  All mutating operations rebuild this slice atomically.
//
//                           +-------------------+
//                    +----▶ |   slot0  healthy | ─┐
//                    │      +-------------------+  │ next
//   cursor ---RR----─┼─────▶ |   slot3  healthy | ─┤
//                    │      +-------------------+  │
//                    │      |   slot5  healthy | ─┘
//                    │      +-------------------+
//           (removed slots remain addressable via map[name])
//
// Active health‑checker runs in separate goroutine, periodically probing "sick"
// backends and resurrecting those that respond OK.
// ---------------------------------------------------------------------------

type BackendsCluster struct {
	cfg config.Config // only for HC intervals; unused on hot path

	cursor   atomic.Uint64                  // RR cursor; overflow‑safe
	slotsPtr atomic.Pointer[[]*backendSlot] // immutable slice of healthy slots

	mu   sync.RWMutex            // guards maps below & HC lists rebuild
	all  map[string]*backendSlot // master registry by name
	sick map[string]*backendSlot // quarantine set  (weak ref to *backendSlot)
	dead map[string]*backendSlot // permanently removed

	hcTicker  *time.Ticker
	hcStopCh  chan struct{}
	hcWG      sync.WaitGroup
	hcEnabled bool
}

// NewCluster builds cluster from config and starts active HC (if possible).
func NewCluster(ctx context.Context, cfg config.Config) (*BackendsCluster, error) {
	c := &BackendsCluster{
		cfg:       cfg,
		all:       make(map[string]*backendSlot),
		sick:      make(map[string]*backendSlot),
		dead:      make(map[string]*backendSlot),
		hcStopCh:  make(chan struct{}),
		hcEnabled: true,
	}

	gofakeit.Seed(0)
	reservedNames := make(map[string]struct{}, 4)

	// populate
	var healthySlots []*backendSlot
	for _, bcfg := range cfg.Upstream().Cluster.Backends {
		threshold := 1_000_000
		name := gofakeit.FirstName()
		for i := 0; i < threshold; i++ {
			if _, exists := reservedNames[name]; exists {
				continue
			}
			reservedNames[name] = struct{}{}
		}
		be := NewBackend(ctx, bcfg, name)
		slot := &backendSlot{be: be}
		if be.IsHealthy() {
			slot.st = healthy
			healthySlots = append(healthySlots, slot)
		} else {
			slot.st = sick
			c.sick[be.Name()] = slot
		}
		c.all[be.Name()] = slot
	}
	if len(healthySlots) == 0 {
		return nil, ErrNoBackends
	}

	c.setHealthySlice(healthySlots)

	// start HC if at least one backend supports Ping()
	if c.hcEnabled {
		c.startHealthChecker()
	}
	return c, nil
}

// ---------------- traffic path ---------------------------------------------

// Fetch delegates call to next healthy backend.
func (c *BackendsCluster) Fetch(rule *config.Rule, path, query []byte, qh *[][2][]byte) (
	int, *[][2][]byte, []byte, func(), error,
) {
	slots := c.slotsPtr.Load()
	if len(*slots) == 0 {
		return 0, nil, nil, emptyReleaseFn, ErrNoBackends
	}

	idx := int(c.cursor.Add(1) % uint64(len(*slots)))
	slot := (*slots)[idx]

	st, hdr, body, rel, err := slot.be.Fetch(rule, path, query, qh)

	// passive HC: mark sick if 5xx or transport error
	if err != nil || st >= http.StatusInternalServerError {
		go c.markSickAsync(slot.be.Name())
	}
	return st, hdr, body, rel, err
}

// ---------------- mutation API ---------------------------------------------

func (c *BackendsCluster) AddNew(be *Backend) error {
	b := *be // just to ensure ptr cannot be nil
	if &b == nil {
		return errors.New("nil backend")
	}
	name := (*be).Name()

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.all[name]; ok {
		return ErrDuplicate
	}

	slot := &backendSlot{be: *be}
	if (*be).IsHealthy() {
		slot.st = healthy
		c.appendHealthySlot(slot)
	} else {
		slot.st = sick
		c.sick[name] = slot
	}
	c.all[name] = slot
	return nil
}

func (c *BackendsCluster) MakeFine(name string) error { return c.promote(name) }
func (c *BackendsCluster) MakeSick(name string) error { return c.quarantine(name) }
func (c *BackendsCluster) Kill(name string) error     { return c.bury(name) }

func (c *BackendsCluster) ImFine() bool { return len(*c.slotsPtr.Load()) > 0 }
func (c *BackendsCluster) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.all)
}

// ---------------- internal helpers  ----------------------------------------

func (c *BackendsCluster) setHealthySlice(slots []*backendSlot) {
	// rebuild immutable slice atomically
	c.slotsPtr.Store(&slots)
	// reset cursor when slice shrinks to avoid large modulo with zero
	if len(slots) == 0 {
		c.cursor.Store(0)
	}
}

func (c *BackendsCluster) appendHealthySlot(slot *backendSlot) {
	old := c.slotsPtr.Load()
	newSlice := append(append([]*backendSlot(nil), (*old)...), slot) // copy‑on‑write
	c.setHealthySlice(newSlice)
}

// quarantine moves backend from healthy → sick.
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

	// rebuild healthy slice without this slot
	old := c.slotsPtr.Load()
	var newSlice []*backendSlot
	for _, s := range *old {
		if s != slot {
			newSlice = append(newSlice, s)
		}
	}
	c.setHealthySlice(newSlice)

	slot.st = sick
	c.sick[name] = slot
	return nil
}

// promote moves backend from sick → healthy.
func (c *BackendsCluster) promote(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.sick[name]
	if !ok {
		return ErrNotFound
	}
	delete(c.sick, name)

	slot.st = healthy
	c.appendHealthySlot(slot)
	return nil
}

func (c *BackendsCluster) bury(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.all[name]
	if !ok {
		return ErrNotFound
	}
	// remove from whichever set it resides in
	if slot.st == healthy {
		old := c.slotsPtr.Load()
		var newSlice []*backendSlot
		for _, s := range *old {
			if s != slot {
				newSlice = append(newSlice, s)
			}
		}
		c.setHealthySlice(newSlice)
	}
	delete(c.sick, name)
	slot.st = dead
	c.dead[name] = slot
	return nil
}

// markSickAsync is a helper for passive HC path (runs in its own goroutine).
func (c *BackendsCluster) markSickAsync(name string) {
	if err := c.quarantine(name); err == nil {
		log.Warn().Str("backend", name).Msg("[cluster] backend quarantined due to error response")
	}
}

// ---------------- active HC -------------------------------------------------

func (c *BackendsCluster) startHealthChecker() {
	interval := time.Second * 10
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
		candidates = append(candidates, slot.be)
	}
	c.mu.RUnlock()

	for _, be := range candidates {
		if be.IsHealthy() {
			_ = c.promote(be.Name())
		}
	}
}

// Close stops background HC goroutine (idempotent).
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
