package upstream

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/rs/zerolog/log"
)

var (
	errNoBackendsAvailable = errors.New("no backends available")
	errBackendNotFound     = errors.New("backend not found")
)

// Cluster is an interface kept for future extension (retry-policies, circuit-breakers etc.).
type Cluster interface {
	Fetch(rule *config.Rule, path, query []byte, queryHeaders *[][2][]byte) (
		status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
	)
	AddNew(b *Backend)
	MakeFine(name string) error
	MakeSick(name string) error
	Kill(name string) error
	ImFine() bool
	Size() int
}

// BackendsCluster is a round-robin cluster with three buckets:
//
//	fine        – healthy backends serving traffic
//	sick  – temporary unhealthy (will be retried by external HC)
//	dead         – permanently removed until restart / manual action
type BackendsCluster struct {
	cur atomic.Int32

	mu   sync.RWMutex
	fine []*Backend
	sick []*Backend
	dead []*Backend
}

// NewBackendsCluster initialises cluster from config, putting only healthy nodes into “fine”.
func NewBackendsCluster(ctx context.Context, cfg config.Config) (*BackendsCluster, error) {
	upstreamCfg := cfg.Upstream()

	var ready []*Backend
	for _, bc := range upstreamCfg.Cluster.Backends {
		be := NewBackend(ctx, bc)
		if be.IsHealthy() {
			ready = append(ready, be)
		} else {
			log.Warn().Str("backend", bc.Name).Msg("[upstream-cluster] backend reported unhealthy on start-up")
		}
	}
	if len(ready) == 0 {
		return nil, errNoBackendsAvailable
	}

	return &BackendsCluster{
		fine: ready,
		sick: make([]*Backend, 0, len(ready)),
		dead: make([]*Backend, 0, len(ready)),
	}, nil
}

// Fetch delegates request to a healthy backend (round-robin).
func (c *BackendsCluster) Fetch(
	rule *config.Rule, path, query []byte, queryHeaders *[][2][]byte,
) (int, *[][2][]byte, []byte, func(), error) {
	be, err := c.nextReady()
	if err != nil {
		return 0, nil, nil, emptyReleaseFn, err
	}
	return be.Fetch(rule, path, query, queryHeaders)
}

// AddNew puts new backend into fine-set if it’s healthy, otherwise into sick.
func (c *BackendsCluster) AddNew(b *Backend) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if b.IsHealthy() {
		c.fine = append(c.fine, b)
	} else {
		c.sick = append(c.sick, b)
	}
}

// MakeSick moves backend from fine → sick.
func (c *BackendsCluster) MakeSick(backend *Backend) error {
	return c.move(backend, &c.fine, &c.sick)
}

// MakeFine moves backend from sick → fine.
func (c *BackendsCluster) MakeFine(backend *Backend) error {
	return c.move(backend, &c.sick, &c.fine)
}

// Kill moves backend to dead bucket (never returns).
func (c *BackendsCluster) Kill(backend *Backend) error {
	// Try to remove from fine
	if err := c.move(backend, &c.fine, &c.dead); err == nil {
		return nil
	}
	// …or from sick
	if err := c.move(backend, &c.sick, &c.dead); err == nil {
		return nil
	}
	return errBackendNotFound
}

// ImFine reports if cluster has at least one fine backend.
func (c *BackendsCluster) ImFine() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.fine) > 0
}

// Size returns total number of *configured* backends (fine+sick+dead).
func (c *BackendsCluster) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.fine) + len(c.sick) + len(c.dead)
}

func (c *BackendsCluster) nextReady() (*Backend, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	n := int32(len(c.fine))
	if n == 0 {
		return nil, errNoBackendsAvailable
	} else if n == 1 {
		return c.fine[0], nil
	}

	// classic round-robin without modulo races
	idx := int(c.cur.Add(1)-1) % int(n)
	return c.fine[idx], nil
}

// move removes backend with given name from src slice and appends to dst slice.
func (c *BackendsCluster) move(backend *Backend, src *[]*Backend, dst *[]*Backend) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, b := range *src {
		if b == backend {
			// delete from src
			*src = append((*src)[:i], (*src)[i+1:]...)
			// add to dst
			*dst = append(*dst, b)
			return nil
		}
	}
	return errBackendNotFound
}
