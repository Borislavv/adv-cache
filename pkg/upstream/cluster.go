// atomic_slot.go
package upstream

import (
	"context"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/brianvoe/gofakeit/v6"
	"github.com/rs/zerolog/log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/valyala/fasthttp"
)

// BackendCluster atomic backend cluster with immutable healthy slice

type BackendCluster struct {
	cursor  atomic.Uint64                  // RR cursor
	healthy atomic.Pointer[[]*backendSlot] // immutable, atomically swapped

	all  atomic.Pointer[map[string]*backendSlot]
	sick atomic.Pointer[map[string]*backendSlot]
	dead atomic.Pointer[map[string]*backendSlot]
}

func NewBackendCluster(ctx context.Context, cfg config.Config) (*BackendCluster, error) {
	c := &BackendCluster{}
	c.healthy.Store(new([]*backendSlot))
	c.all.Store(&map[string]*backendSlot{})
	c.sick.Store(&map[string]*backendSlot{})
	c.dead.Store(&map[string]*backendSlot{})

	for _, backendCfg := range cfg.Upstream().Cluster.Backends {
		if ID, err := c.Add(ctx, backendCfg); err != nil {
			log.Error().Msgf("[upstream-cluster] failed to add new one backend (%s) into cluster: %s", backendCfg.Host, err.Error())
			return nil, err
		} else {
			log.Info().Msgf("[upstream-cluster] added new one backend '%s' (%s) into cluster", ID, backendCfg.Host)
		}
	}

	return c, nil
}

func (c *BackendCluster) Run(ctx context.Context) {
	quarantineCh := make(chan string, 256)
	hcTicker := utils.NewTicker(ctx, 10*time.Second)
	killerTicker := utils.NewTicker(ctx, 24*time.Hour)

	c.runMedic(ctx, quarantineCh)
	c.runHealthChecker(ctx, hcTicker)
	c.runKiller(ctx, killerTicker)
}

func (c *BackendCluster) getAll() map[string]*backendSlot {
	return *c.all.Load()
}

func (c *BackendCluster) getSick() map[string]*backendSlot {
	return *c.sick.Load()
}

func (c *BackendCluster) getDead() map[string]*backendSlot {
	return *c.dead.Load()
}

func (c *BackendCluster) appendHealthySlot(s *backendSlot) {
	for {
		old := c.healthy.Load()
		slice := append(append([]*backendSlot(nil), *old...), s)
		if c.healthy.CompareAndSwap(old, &slice) {
			return
		}
	}
}

func (c *BackendCluster) removeHealthySlot(target *backendSlot) {
	for {
		old := c.healthy.Load()
		slice := make([]*backendSlot, 0, len(*old)-1)
		for _, s := range *old {
			if s != target {
				slice = append(slice, s)
			}
		}
		if c.healthy.CompareAndSwap(old, &slice) {
			return
		}
	}
}

func (c *BackendCluster) Quarantine(id string) bool {
	all := c.getAll()
	slot, ok := all[id]
	if !ok || !slot.TryTransition(stateHealthy, stateSick) {
		return false
	}
	c.removeHealthySlot(slot)
	for {
		old := c.sick.Load()
		copy := make(map[string]*backendSlot, len(*old)+1)
		for k, v := range *old {
			copy[k] = v
		}
		copy[id] = slot
		if c.sick.CompareAndSwap(old, &copy) {
			return true
		}
	}
}

func (c *BackendCluster) Promote(id string) bool {
	sick := c.getSick()
	slot, ok := sick[id]
	if !ok || !slot.TryTransition(stateSick, stateHealthy) {
		return false
	}
	for {
		old := c.sick.Load()
		copy := make(map[string]*backendSlot, len(*old)-1)
		for k, v := range *old {
			if k != id {
				copy[k] = v
			}
		}
		if c.sick.CompareAndSwap(old, &copy) {
			break
		}
	}
	c.appendHealthySlot(slot)
	return true
}

func (c *BackendCluster) Kill(id string) bool {
	all := c.getAll()
	slot, ok := all[id]
	if !ok {
		return false
	}
	for {
		st := slot.State()
		if st == stateDead {
			return false
		}
		if slot.TryTransition(st, stateDead) {
			break
		}
	}
	c.removeHealthySlot(slot)
	for {
		old := c.dead.Load()
		copy := make(map[string]*backendSlot, len(*old)+1)
		for k, v := range *old {
			copy[k] = v
		}
		copy[id] = slot
		if c.dead.CompareAndSwap(old, &copy) {
			return true
		}
	}
	return false
}

// Add create and set a new backend. Returns and ID or error.
func (c *BackendCluster) Add(ctx context.Context, cfg *config.Backend) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("invalid backend config")
	}
	for tries := 0; tries < 100_000; tries++ {
		candidateID := strings.ToLower(gofakeit.FirstName())
		old := c.all.Load()
		if _, exists := (*old)[candidateID]; exists {
			continue
		}
		copy := make(map[string]*backendSlot, len(*old)+1)
		for k, v := range *old {
			copy[k] = v
		}
		be := NewBackend(ctx, cfg, candidateID)
		slot := newBackendSlot(ctx, be)
		copy[candidateID] = slot
		if c.all.CompareAndSwap(old, &copy) {
			if err := be.IsHealthy(); err == nil {
				slot.state.Store(uint32(stateHealthy))
				c.appendHealthySlot(slot)
			} else {
				slot.state.Store(uint32(stateSick))
				for {
					oldSick := c.sick.Load()
					copySick := make(map[string]*backendSlot, len(*oldSick)+1)
					for k, v := range *oldSick {
						copySick[k] = v
					}
					copySick[candidateID] = slot
					if c.sick.CompareAndSwap(oldSick, &copySick) {
						break
					}
				}
			}
			return candidateID, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique backend id")
}

func (c *BackendCluster) Update(ctx context.Context, id string, cfg *config.Backend) error {
	if cfg == nil {
		return fmt.Errorf("nil backend config")
	}
	for {
		old := c.all.Load()
		slot, ok := (*old)[id]
		if !ok {
			return fmt.Errorf("backend not found")
		}
		newAll := make(map[string]*backendSlot, len(*old))
		for k, v := range *old {
			newAll[k] = v
		}
		backend := NewBackend(ctx, cfg, id)
		slot.backend = backend
		if c.all.CompareAndSwap(old, &newAll) {
			return nil
		}
	}
}

func (c *BackendCluster) Remove(id string) bool {
	all := c.getAll()
	slot, ok := all[id]
	if !ok {
		return false
	}
	for {
		old := c.all.Load()
		newMap := make(map[string]*backendSlot, len(*old)-1)
		for k, v := range *old {
			if k != id {
				newMap[k] = v
			}
		}
		if c.all.CompareAndSwap(old, &newMap) {
			break
		}
	}
	c.removeHealthySlot(slot)
	return true
}

func (c *BackendCluster) markSickAsync(id string) {
	_ = c.Quarantine(id) // non-blocking, best-effort
}

func (c *BackendCluster) Fetch(rule *config.Rule, ctx *fasthttp.RequestCtx, r *fasthttp.Request) (
	req *fasthttp.Request, resp *fasthttp.Response,
	releaser func(*fasthttp.Request, *fasthttp.Response), err error,
) {
	slots := c.healthy.Load()
	if len(*slots) == 0 {
		return nil, nil, emptyReleaserFn, ErrNoBackends
	}
	slotsLen := uint64(len(*slots))
	i := uint64(0)
	for {
		if i >= slotsLen {
			break
		}
		slot := (*slots)[int(c.cursor.Add(1)%slotsLen)]
		select {
		case <-slot.rate.Chan():
			req, resp, releaser, err = slot.backend.Fetch(rule, ctx, r)
			if err != nil || resp.StatusCode() >= 500 {
				c.markSickAsync(slot.backend.ID())
			}
			return req, resp, releaser, err
		default:
			i++
		}
	}
	return nil, nil, emptyReleaserFn, ErrAllBackendsAreBusy
}

func (c *BackendCluster) SnapshotMetrics() string {
	b := &strings.Builder{}
	b.WriteString("# HELP backend_state Backend State: 1=present, 0=absent\n")
	b.WriteString("# TYPE backend_state gauge\n")
	for name, slot := range c.getAll() {
		for _, st := range []slotState{stateHealthy, stateSick, stateDead} {
			val := 0
			if slot.State() == st {
				val = 1
			}
			fmt.Fprintf(b, "backend_state{backend=\"%s\",state=\"%s\"} %d\n", name, st.String(), val)
		}
	}
	return b.String()
}

func (c *BackendCluster) runMedic(ctx context.Context, quarantineCh <-chan string) {
	log.Info().Msg("[upstream-medic] started")
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-quarantineCh:
				_ = c.Quarantine(id) // best-effort
				log.Info().Msgf("[upstream-medic] backend '%s' was moved to quarantine", id)
			}
		}
	}()
}

func (c *BackendCluster) runKiller(ctx context.Context, ticker <-chan time.Time) {
	log.Info().Msg("[upstream-killer] started")
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker:
				count := c.killDoomedBackends()
				log.Info().Msgf("[upstream-killer] '%d' were killed", count)
			}
		}
	}()
}

func (c *BackendCluster) runHealthChecker(ctx context.Context, ticker <-chan time.Time) {
	log.Info().Msg("[upstream-healthchecker] started")
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker:
				h, s, d := c.probeSickBackends()
				log.Info().Msgf("[upstream-healthchecker] healthy: %d, sick: %d, dead: %d", h, s, d)
			}
		}
	}()
}

func (c *BackendCluster) probeSickBackends() (healthy, sick, dead int) {
	sickMap := c.getSick()
	for id, slot := range sickMap {
		go func(id string, slot *backendSlot) {
			if err := slot.backend.IsHealthy(); err == nil {
				c.Promote(id)
			} else {
				if slot.hcRetries.Add(1) >= 60 {
					c.Kill(id)
				}
			}
		}(id, slot)
	}
	return len(*c.healthy.Load()), len(sickMap), len(c.getDead())
}

func (c *BackendCluster) killDoomedBackends() int {
	deadMap := c.getDead()
	count := 0
	for id := range deadMap {
		if c.Remove(id) {
			count++
		}
	}
	return count
}
