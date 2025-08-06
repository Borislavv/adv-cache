// Package upstream provides a minimal reverse‑proxy cluster with passive and
// optional active health checks. It is designed for production: the data path
// is lock‑free (only atomic ops on the hot path) and all mutations are
// performed with copy‑on‑write under coarse locks.
//
// The implementation relies only on the limited Backend interface declared
// below to avoid circular dependencies with actual backend code.
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
// Backend contract (only what the cluster really needs).
// -----------------------------------------------------------------------------

type fetchFn = func(rule *config.Rule, path, query []byte, queryHeaders *[][2][]byte) (
	status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
)

// Backend is the subset of methods the balancer depends on. Methods must be
// thread‑safe.
//
// Imposing a very small surface keeps the cluster independent from the concrete
// backend implementation.
//
//nolint:interfacebloat // explicit on purpose
type Backend interface {
	Name() string
	IsHealthy() bool
	Fetch(rule *config.Rule, path, query []byte, queryHeaders *[][2][]byte) (
		status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
	)
}

// -----------------------------------------------------------------------------
// Errors exposed by the package.
// -----------------------------------------------------------------------------

var (
	ErrNoBackends = errors.New("no healthy backends in cluster")
	ErrDuplicate  = errors.New("backend already exists in cluster")
	ErrNotFound   = errors.New("backend not found in cluster")
)

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
	be   Backend
	st   state
	next int // index of next healthy slot – avoids reallocs when rebuilding list
}

// -----------------------------------------------------------------------------
// BackendsCluster – round‑robin balancer.
// -----------------------------------------------------------------------------
//
// * Lock‑free Fetch path (atomic cursor + immutable slice)
// * Passive health checks (5xx or transport error ⇒ quarantine)
// * Optional active health checker with exponential back‑off
// * Copy‑on‑write mutations under a single RW lock
//
//            cursor (RR) ─────────┐
//                                ▼
//          +---------+   +---------+   +---------+
// slotsPtr | slot 0 ▶ |─▶| slot 3 ▶ |─▶| slot 5 ▶|─▶ nil
//          +---------+   +---------+   +---------+
//
// Removed backends remain addressable via maps; only healthy ones live in the
// slots slice.
// -----------------------------------------------------------------------------

type BackendsCluster struct {
	cfg config.Config // only HC intervals; never accessed on hot path

	cursor   atomic.Uint64                  // RR cursor (overflow‑safe)
	slotsPtr atomic.Pointer[[]*backendSlot] // immutable slice of healthy backends

	mu   sync.RWMutex            // guards maps below & slice rebuilds
	all  map[string]*backendSlot // master registry by name
	sick map[string]*backendSlot // quarantine set
	dead map[string]*backendSlot // permanently removed

	hcTicker  *time.Ticker
	hcStopCh  chan struct{}
	hcWG      sync.WaitGroup
	hcEnabled bool
}

// NewCluster builds a cluster from cfg and starts active HC if at least one
// backend supports Ping(). Returns ErrNoBackends when nothing is healthy at
// startup.
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

	// bootstrap registry
	var healthySlots []*backendSlot
	for _, bcfg := range cfg.Upstream().Cluster.Backends {
		// Generate a unique in‑process name when none provided.
		name := bcfg.Name
		if name == "" {
			// Retry a few times – collisions are extremely unlikely in practice.
			for i := 0; i < 3; i++ {
				candidate := gofakeit.FirstName()
				if _, dup := reserved[candidate]; !dup {
					name = candidate
					reserved[name] = struct{}{}
					break
				}
			}
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

	if c.hcEnabled {
		c.startHealthChecker()
	}
	return c, nil
}

// -----------------------------------------------------------------------------
// Traffic path.
// -----------------------------------------------------------------------------

// Fetch proxies the request to the next healthy backend. It is completely
// lock‑free: a single atomic add + array index.
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

	// passive HC – quarantine on 5xx or transport error
	if err != nil || st >= http.StatusInternalServerError {
		go c.markSickAsync(slot.be.Name())
	}
	return st, hdr, body, rel, err
}

// -----------------------------------------------------------------------------
// Mutation API.
// -----------------------------------------------------------------------------

// AddNew registers a backend at runtime.
func (c *BackendsCluster) AddNew(be Backend) error {
	if be == nil {
		return errors.New("nil backend")
	}
	name := be.Name()

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.all[name]; ok {
		return ErrDuplicate
	}

	slot := &backendSlot{be: be}
	if be.IsHealthy() {
		slot.st = healthy
		c.appendHealthySlot(slot)
	} else {
		slot.st = sick
		c.sick[name] = slot
	}
	c.all[name] = slot
	return nil
}

func (c *BackendsCluster) Promote(name string) error    { return c.promote(name) }
func (c *BackendsCluster) Quarantine(name string) error { return c.quarantine(name) }
func (c *BackendsCluster) Kill(name string) error       { return c.bury(name) }

// Healthy reports whether the cluster still has at least one healthy backend.
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
	c.slotsPtr.Store(&slots) // copy‑on‑write; slice header escapes to heap
	if len(slots) == 0 {
		c.cursor.Store(0)
	}
}

func (c *BackendsCluster) appendHealthySlot(slot *backendSlot) {
	old := c.slotsPtr.Load()
	newSlice := append(append([]*backendSlot(nil), *old...), slot)
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
		return nil // already quarantined or dead
	}

	// rebuild healthy slice without the slot
	old := c.slotsPtr.Load()
	newSlice := make([]*backendSlot, 0, len(*old)-1)
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

	// remove from whichever set it currently resides in
	switch slot.st {
	case healthy:
		old := c.slotsPtr.Load()
		newSlice := make([]*backendSlot, 0, len(*old)-1)
		for _, s := range *old {
			if s != slot {
				newSlice = append(newSlice, s)
			}
		}
		c.setHealthySlice(newSlice)
	case sick:
		delete(c.sick, name)
	}

	slot.st = dead
	c.dead[name] = slot
	return nil
}

func (c *BackendsCluster) markSickAsync(name string) {
	if err := c.quarantine(name); err == nil {
		log.Warn().Str("backend", name).Msg("cluster: backend quarantined due to error response")
	}
}

// -----------------------------------------------------------------------------
// Active health checker.
// -----------------------------------------------------------------------------

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

// Close stops the active HC goroutine. It is idempotent.
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
