package upstream

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/Borislavv/advanced-cache/pkg/config"
)

var (
	ErrNoHealthyBackends = errors.New("no healthy backends available")
	ErrNoFreeBackends    = errors.New("all backends are busy")
)

type BackendCluster struct {
	ctx     context.Context
	cfg     config.Config
	cursor  atomic.Uint64
	healthy atomic.Pointer[[]*backendSlot] // immutable healthy list

	mu   sync.RWMutex // protects copy-on-write mutations
	all  map[string]*backendSlot
	sick map[string]*backendSlot
	dead map[string]*backendSlot

	freeBackCh <-chan *backendSlot
}

func Cluster(ctx context.Context, cfg config.Config) (*BackendCluster, error) {
	length := len(cfg.Upstream().Cluster.Backends)
	c := &BackendCluster{
		ctx: ctx, cfg: cfg,
		all:  make(map[string]*backendSlot, length),
		sick: make(map[string]*backendSlot),
		dead: make(map[string]*backendSlot),
	}

	slots := make([]*backendSlot, 0, length)
	for _, backendCfg := range cfg.Upstream().Cluster.Backends {
		var slot *backendSlot
		backend := NewBackend(ctx, backendCfg)
		if err := PrewarmBackend(ctx, backend); err != nil {
			slot = newBackendSlot(ctx, backend, stateSick)
			log.Warn().Err(err).Str("id", backend.ID()).Msgf("[upstream-init] slot '%s' added into cluster as sick", backend.id)
		} else {
			slot = newBackendSlot(ctx, backend, stateHealthy)
			log.Info().Str("id", backend.ID()).Msgf("[upstream-init] slot '%s' added into cluster as healthy", backend.id)
		}

		c.all[backend.ID()] = slot
		slots = append(slots, slot)
	}
	if len(slots) == 0 {
		return nil, ErrNoHealthyBackends
	}
	c.healthy.Store(&slots)

	SetGaugeHealthy(len(slots))
	StartHealthChecks(ctx, c)
	c.freeBackCh = c.provideFreeBackends()

	return c, nil
}

func (c *BackendCluster) provideFreeBackends() <-chan *backendSlot {
	outCh := make(chan *backendSlot, runtime.GOMAXPROCS(0)*4)
	for _, slot := range *c.healthy.Load() {
		go func(slot *backendSlot) {
			for range slot.rate.Load().Chan() {
				outCh <- slot
			}
		}(slot)
	}
	return outCh
}

func (c *BackendCluster) getFreeBackend() (*backendSlot, error) {
	// TODO make switcher through cfg
	return <-c.freeBackCh, nil
	//select {
	//case slot := <-c.freeBackCh:
	//	return slot, nil
	//default:
	//	return nil, ErrNoFreeBackends
	//}
}

func (c *BackendCluster) Fetch(rule *config.Rule, ctx *fasthttp.RequestCtx, req *fasthttp.Request) (*fasthttp.Request, *fasthttp.Response, func(*fasthttp.Request, *fasthttp.Response), error) {
	if slot, err := c.getFreeBackend(); err != nil {
		return nil, nil, defaultReleaser, err
	} else {
		slot.reqCount.Add(1)
		outReq, outResp, done, ferr := slot.backend.Fetch(rule, ctx, req)
		if ferr != nil {
			slot.errCount.Add(1)
		}
		return outReq, outResp, done, ferr
	}
}

func (c *BackendCluster) Promote(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.sick[id]
	if !ok {
		return fmt.Errorf("backend '%s' not found in sick list", id)
	}
	delete(c.sick, id)
	slot.promote()
	c.addToHealthy(slot)
	return nil
}

func (c *BackendCluster) Quarantine(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.all[id]
	if !ok || !slot.stateCAS(stateHealthy, stateSick) {
		return fmt.Errorf("cannot quarantine backend %q", id)
	}
	c.removeFromHealthy(slot)
	slot.quarantine()
	c.sick[id] = slot
	IncGaugeSick()
	DecGaugeHealthy()
	return nil
}

func (c *BackendCluster) Kill(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.sick[id]
	if !ok || !slot.stateCAS(stateSick, stateDead) {
		return fmt.Errorf("cannot kill backend %q", id)
	}
	delete(c.sick, id)
	slot.kill()
	c.dead[id] = slot
	IncGaugeDead()
	DecGaugeSick()
	log.Info().Str("id", id).Msgf("[upstream-healthcheck] backend '%s' killed", id)
	return nil
}

func (c *BackendCluster) Resurrect(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.dead[id]
	if !ok || !slot.stateCAS(stateDead, stateHealthy) {
		return fmt.Errorf("cannot resurrect backend %q", id)
	}
	delete(c.dead, id)
	slot.resurrect()
	c.addToHealthy(slot)
	DecGaugeDead()
	IncGaugeHealthy()
	log.Info().Str("id", id).Msgf("[upstream-healthcheck] backend '%s' resurrected", id)
	return nil
}

func (c *BackendCluster) Bury(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.dead[id]
	if !ok || !slot.stateCAS(stateDead, stateBuried) {
		return fmt.Errorf("cannot bury backend %q", id)
	}
	delete(c.dead, id)
	delete(c.all, id)
	slot.bury()
	DecGaugeDead()
	IncGaugeBuried()
	log.Info().Str("id", id).Msgf("[upstream-killer] backend '%s' buried", id)
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
