package upstream

import (
	"context"
	"errors"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
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
	ErrNoRatesConfigured    = errors.New("no backend rates configured")
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
	mu  sync.RWMutex            // mutex under all map
	all map[string]*backendSlot // slots by backend id
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
	slot.hotPathCounters.total.Add(1)
	outReq, outResp, releaser, err = slot.backend.Fetch(rule, inCtx, inReq)
	if err != nil || outResp.StatusCode() >= fasthttp.StatusInternalServerError {
		slot.hotPathCounters.errors.Add(1)
	} else if outResp.StatusCode() >= fasthttp.StatusUnprocessableEntity {
		DumpRequestInfo(inCtx)
	}
	return
}

func (c *BackendCluster) hasNotHealthyBackends() bool {
	return healthyBackends.Load() <= 0
}

func (c *BackendCluster) shouldWaitAvailableSlot() bool {
	return c.isAwaitPolicy.Load()
}

func (c *BackendCluster) initBackends(ctx context.Context, backendsCfg []*config.Backend) error {
	numBackends := len(backendsCfg)
	if numBackends == 0 {
		return ErrNoBackendsConfigured
	}

	slotsChCap := 0
	for i := 0; i < numBackends; i++ {
		slotsChCap += backendsCfg[i].Rate
	}
	if slotsChCap <= 0 {
		return ErrNoRatesConfigured
	}

	allSlotsByID := make(map[string]*backendSlot, numBackends)
	slotsCh := make(chan *backendSlot, slotsChCap)

	for _, backendCfg := range backendsCfg {
		if !backendCfg.Enabled {
			continue
		}

		if _, found := allSlotsByID[backendCfg.ID]; found {
			return fmt.Errorf("config has duplicate backends '%s'", backendCfg.ID)
		}

		allSlotsByID[backendCfg.ID] = newBackendSlot(ctx, backendCfg, slotsCh)
		slot, found := allSlotsByID[backendCfg.ID]
		if !found {
			return fmt.Errorf("cannot find added backend '%s'", backendCfg.ID)
		}

		if healthcheckErr := slot.probe(); healthcheckErr != nil {
			if slot.quarantine("failed probe while init.", true) {
				log.Warn().Msgf("[upstream] backend '%s' add as sick: %s", slot.backend.Name(), healthcheckErr.Error())
			} else {
				return fmt.Errorf("workflow error, failed to move new backend '%s' into quarantine", slot.backend.Name())
			}
			continue
		}

		log.Info().Msgf("[upstream] backend '%s' add as healthy", slot.backend.Name())
	}
	if len(allSlotsByID) == 0 {
		return ErrNoHealthyBackends
	}

	c.all = allSlotsByID
	c.slotCh = slotsCh

	return nil
}
