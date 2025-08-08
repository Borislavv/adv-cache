package upstream

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"

	"github.com/Borislavv/advanced-cache/pkg/config"
)

var (
	ErrNoHealthyBackends = errors.New("no healthy backends available")
)

type BackendCluster struct {
	cursor  atomic.Uint64
	healthy atomic.Pointer[[]*backendSlot] // immutable healthy list

	mu   sync.RWMutex // protects copy-on-write mutations
	all  map[string]*backendSlot
	sick map[string]*backendSlot
	dead map[string]*backendSlot
}

func Cluster(ctx context.Context, cfg config.Config) (*BackendCluster, error) {
	length := len(cfg.Upstream().Cluster.Backends)
	c := &BackendCluster{
		all:  make(map[string]*backendSlot, length),
		sick: make(map[string]*backendSlot),
		dead: make(map[string]*backendSlot),
	}

	slots := make([]*backendSlot, 0, length)
	for _, backendCfg := range cfg.Upstream().Cluster.Backends {
		s := newBackendSlot(NewBackend(ctx, backendCfg))
		c.all[s.backend.ID()] = s
		slots = append(slots, s)
	}
	if len(slots) == 0 {
		return nil, ErrNoHealthyBackends
	}
	c.healthy.Store(&slots)
	SetGaugeHealthy(len(slots))

	return c, nil
}

func (c *BackendCluster) Fetch(rule *config.Rule, ctx *fasthttp.RequestCtx, req *fasthttp.Request) (*fasthttp.Request, *fasthttp.Response, func(*fasthttp.Request, *fasthttp.Response), error) {
	slots := c.healthy.Load()
	if len(*slots) == 0 {
		return nil, nil, nil, ErrNoHealthyBackends
	}

	cursor := c.cursor.Add(1)
	start := int(cursor % uint64(len(*slots)))

	for i := 0; i < len(*slots); i++ {
		s := (*slots)[(start+i)%len(*slots)]
		if s.rateLimiter.Throttle() {
			continue
		}
		outReq, outResp, done, err := s.backend.Fetch(rule, ctx, req)
		s.Track(err)
		return outReq, outResp, done, err
	}
	return nil, nil, nil, ErrNoHealthyBackends
}

func (c *BackendCluster) Promote(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.sick[id]
	if !ok {
		return fmt.Errorf("backend %q not found in sick list", id)
	}
	delete(c.sick, id)
	c.addToHealthy(slot)
	slot.promote()
	return nil
}

func (c *BackendCluster) Quarantine(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.all[id]
	if !ok || !slot.casState(stateHealthy, stateSick) {
		return fmt.Errorf("cannot quarantine backend %q", id)
	}
	c.removeFromHealthy(slot)
	c.sick[id] = slot
	IncGaugeSick()
	DecGaugeHealthy()
	log.Info().Str("id", id).Msg("[upstream-cluster] backend quarantined")
	return nil
}

func (c *BackendCluster) Kill(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.sick[id]
	if !ok || !slot.casState(stateSick, stateDead) {
		return fmt.Errorf("cannot kill backend %q", id)
	}
	delete(c.sick, id)
	c.dead[id] = slot
	IncGaugeDead()
	DecGaugeSick()
	log.Info().Str("id", id).Msg("[upstream-healthcheck] backend killed")
	return nil
}

func (c *BackendCluster) Resurrect(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.dead[id]
	if !ok || !slot.casState(stateDead, stateHealthy) {
		return fmt.Errorf("cannot resurrect backend %q", id)
	}
	delete(c.dead, id)
	c.addToHealthy(slot)
	DecGaugeDead()
	IncGaugeHealthy()
	log.Info().Str("id", id).Msg("[upstream-healthcheck] backend resurrected")
	return nil
}

func (c *BackendCluster) Bury(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.dead[id]
	if !ok || !slot.casState(stateDead, stateBuried) {
		return fmt.Errorf("cannot bury backend %q", id)
	}
	delete(c.dead, id)
	delete(c.all, id)
	DecGaugeDead()
	IncGaugeBuried()
	log.Info().Str("id", id).Msg("[upstream-killer] backend buried")
	return nil
}

func (c *BackendCluster) addToHealthy(slot *backendSlot) {
	old := c.healthy.Load()
	copied := make([]*backendSlot, len(*old)+1)
	copy(copied, *old)
	copied[len(*old)] = slot
	c.healthy.Store(&copied)
}

func (c *BackendCluster) removeFromHealthy(slot *backendSlot) {
	old := c.healthy.Load()
	n := make([]*backendSlot, 0, len(*old)-1)
	for _, s := range *old {
		if s != slot {
			n = append(n, s)
		}
	}
	c.healthy.Store(&n)
}
