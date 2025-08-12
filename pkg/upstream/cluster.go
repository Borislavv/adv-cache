package upstream

import (
	"context"
	"errors"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"runtime"
	"sync"
	"sync/atomic"
)

type policy string

const (
	deny  policy = "deny"
	await policy = "await"
)

var (
	ErrNoBackendsConfigured = errors.New("no backends configured")
	ErrNoHealthyBackends    = errors.New("no healthy backends")
	ErrAllBackendsAreBusy   = errors.New("all backends are busy")
)

type BackendCluster struct {
	ctx context.Context

	// should we wait until rateLimit limiter will emit a new one token and slot will be available?
	isAwaitPolicy atomic.Bool

	// rateLimit limited chan which provides random available backend slot
	slotCh <-chan *backendSlot

	// has all backends by their IDs
	mu  sync.RWMutex
	all map[string]*backendSlot
}

func NewBackendCluster(ctx context.Context, cfg config.Config) (*BackendCluster, error) {
	cluster := &BackendCluster{
		ctx:           ctx,
		isAwaitPolicy: atomic.Bool{},
	}

	if err := cluster.initBackends(ctx, cfg.Upstream().Cluster.Backends); err != nil {
		return nil, err
	} else {
		cluster.isAwaitPolicy.Store(policy(cfg.Upstream().Policy) == await)
		go cluster.monitor(ctx)
	}

	return cluster, nil
}

// Fetch - proxy method which takes first allowed backend and do request.
// Errors:
// 1. ErrNoHealthyBackends
// 2. ErrAllBackendsAreBusy
func (c *BackendCluster) Fetch(rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (
	outReq *fasthttp.Request, outResp *fasthttp.Response, releaser func(*fasthttp.Request, *fasthttp.Response), err error,
) {
	if c.hasNotHealthyBackends() {
		return nil, nil, defaultReleaser, ErrNoHealthyBackends
	}

	if c.shouldWaitAvailableSlot() {
		return c.fetch(<-c.slotCh, rule, inCtx, inReq)
	}

	select {
	case slot := <-c.slotCh:
		return c.fetch(slot, rule, inCtx, inReq)
	default:
		return nil, nil, defaultReleaser, ErrAllBackendsAreBusy
	}
}

func (c *BackendCluster) fetch(slot *backendSlot, rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (
	outReq *fasthttp.Request, outResp *fasthttp.Response, releaser func(*fasthttp.Request, *fasthttp.Response), err error,
) {
	slot.total.Add(1)
	outReq, outResp, releaser, err = slot.backend.Fetch(rule, inCtx, inReq)
	if err != nil || outResp.StatusCode() > fasthttp.StatusInternalServerError {
		slot.errors.Add(1)
	}
	return
}

func (c *BackendCluster) hasNotHealthyBackends() bool {
	return healthyBackendsNum.Load() <= 0
}

func (c *BackendCluster) shouldWaitAvailableSlot() bool {
	return c.isAwaitPolicy.Load()
}

func (c *BackendCluster) initBackends(ctx context.Context, backendsCfg []*config.Backend) error {
	numBackends := len(backendsCfg)
	if numBackends == 0 {
		return ErrNoBackendsConfigured
	}

	all := make(map[string]*backendSlot, numBackends)
	slotCh := make(chan *backendSlot, runtime.GOMAXPROCS(0)*4)
	c.slotCh = slotCh
	c.all = all

	for _, backendCfg := range backendsCfg {
		slot := newBackendSlot(ctx, backendCfg, slotCh)
		name := slot.backend.Name()
		if healthcheckErr := slot.probe(); healthcheckErr != nil {
			slot.quarantine()
			log.Warn().Msgf("[upstream] backend '%s' add as sick: %s", name, healthcheckErr.Error())
		} else {
			log.Info().Msgf("[upstream] backend '%s' add as healthy", name)
		}
		all[name] = slot
	}
	if len(all) == 0 {
		return ErrNoHealthyBackends
	}

	return nil
}
