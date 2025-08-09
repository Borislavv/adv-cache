package cluster

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

var (
	ErrNoBackends   = errors.New("no healthy backends")
	ErrStateChanged = errors.New("state already changed")
)

// Cluster implements lock-free hot path Fetch with P2C + RR fallback.
type Cluster struct {
	// hot path: immutable slice pointer swapped atomically on updates
	healthy atomic.Pointer[[]*slot]

	// rr cursor for fallback / sampling
	rr atomic.Uint64

	// COW control path
	all map[string]*slot
}

func New(ctx context.Context, cfg config.Config) (*Cluster, error) {
	all := make(map[string]*slot, len(cfg.Upstream().Cluster.Backends))
	healthy := make([]*slot, 0, len(cfg.Upstream().Cluster.Backends))

	for _, bCfg := range cfg.Upstream().Cluster.Backends {
		lim := newTokenLimiter(bCfg.Rate, max(1, bCfg.Rate/10)) // if rate is zero -> disabled
		b := NewBackend(ctx, bCfg)
		s := &slot{be: b, lim: lim}
		s.state.Store(int32(Healthy))
		s.effective.Store(uint32(bCfg.Rate))
		all[(*b).Name()] = s
		healthy = append(healthy, s)
	}

	var c Cluster
	c.all = all
	c.healthy.Store(&healthy)
	go c.runSickMonitor(ctx)
	go c.runDeadMonitor(ctx)
	go c.runThrottleMonitor(ctx)
	go c.runHealthyIdleMonitor(ctx)

	log.Info().Msg("[app] upstream cluster was initialized")
	return &c, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Fetch is the hot path: 0 alloc, 0 locks.
func (c *Cluster) Fetch(rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (
	outReq *fasthttp.Request, outResp *fasthttp.Response, releaser func(*fasthttp.Request, *fasthttp.Response), err error,
) {
	ptr := c.healthy.Load()
	if ptr == nil || len(*ptr) == 0 {
		return nil, nil, defaultReleaser, ErrNoBackends
	}
	hs := *ptr

	// P2C: pick two random indices based on rr cursor for cache friendliness
	n := uint64(len(hs))
	cur := c.rr.Add(1)
	i := int(cur % n)
	j := int((cur*2862933555777941757 + 3037000493) % n) // LCG mix to decorrelate

	a, b := hs[i], hs[j]
	// choose the one with fewer active requests approximated by token deficit (more tokens => less load)
	// We don't keep active counter; use instantaneous token snapshot as cheap signal.
	var pick *slot
	if a.lim.tokens.Load() >= b.lim.tokens.Load() {
		pick = a
	} else {
		pick = b
	}

	now := time.Now().UnixNano()
	if !pick.lim.allow(now) {
		// fast RR fallback: one probe
		k := int((cur + 1) % n)
		alt := hs[k]
		if !alt.lim.allow(now) {
			return nil, nil, defaultReleaser, fasthttp.ErrNoFreeConns
		}
		pick = alt
	}

	outReq, outResp, releaser, err = pick.be.Fetch(rule, inCtx, inReq)
	if err != nil || outResp.StatusCode() > http.StatusInternalServerError {
		pick.recordOutcome(now, false)
	} else {
		pick.recordOutcome(now, true)
	}
	return outReq, outResp, releaser, err
}

// --- Control-plane methods (COW, no hot-path locks) ---

func (c *Cluster) Promote(name string) error {
	s, ok := c.all[name]
	if !ok {
		return errors.New("backend not found")
	}
	if !casState(&s.state, Sick, Healthy) && !casState(&s.state, Dead, Healthy) {
		return ErrStateChanged
	}
	log.Info().Str("backend", name).Str("to", Healthy.String()).Msg("[upstream-cluster] promote")
	// ensure in healthy slice
	for {
		cur := c.healthy.Load()
		cp := append([]*slot(nil), (*cur)...)
		if !contains(cp, s) {
			cp = append(cp, s)
		}
		if c.healthy.CompareAndSwap(cur, &cp) {
			break
		}
	}
	return nil
}

func (c *Cluster) Quarantine(name string) error {
	s, ok := c.all[name]
	if !ok {
		return errors.New("backend not found")
	}
	if !casState(&s.state, Healthy, Sick) {
		return ErrStateChanged
	}
	log.Warn().Str("backend", name).Str("to", Sick.String()).Msg("[upstream-cluster] quarantine")
	// remove from healthy
	for {
		cur := c.healthy.Load()
		cp := filterOut(*cur, s)
		if c.healthy.CompareAndSwap(cur, &cp) {
			break
		}
	}
	return nil
}

func (c *Cluster) Kill(name string) error {
	s, ok := c.all[name]
	if !ok {
		return errors.New("backend not found")
	}
	// Healthy/Sick -> Dead
	if !(casState(&s.state, Healthy, Dead) || casState(&s.state, Sick, Dead)) {
		return ErrStateChanged
	}
	// remove from healthy if present
	for {
		cur := c.healthy.Load()
		cp := filterOut(*cur, s)
		if c.healthy.CompareAndSwap(cur, &cp) {
			break
		}
	}
	log.Error().Str("backend", name).Str("to", Dead.String()).Msg("[upstream-cluster] kill")
	return nil
}

func (c *Cluster) Bury(name string) error {
	s, ok := c.all[name]
	if !ok {
		return errors.New("backend not found")
	}
	if !casState(&s.state, Dead, Buried) {
		return ErrStateChanged
	}
	log.Error().Str("backend", name).Str("to", Buried.String()).Msg("[upstream-cluster] bury")
	return nil
}

func contains(ss []*slot, s *slot) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func filterOut(ss []*slot, s *slot) []*slot {
	out := make([]*slot, 0, len(ss))
	for _, x := range ss {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// shuffleHealthy is used in slow-start or to break stickiness under imbalance.
func (c *Cluster) shuffleHealthy() {
	cur := c.healthy.Load()
	if cur == nil || len(*cur) < 2 {
		return
	}
	cp := append([]*slot(nil), (*cur)...)
	rand.Shuffle(len(cp), func(i, j int) { cp[i], cp[j] = cp[j], cp[i] })
	c.healthy.Store(&cp)
}
