package storage

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/rate"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/lru"
	"runtime"
	"strconv"
	"time"

	"github.com/Borislavv/traefik-http-cache-plugin/pkg/config"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/utils"
	"github.com/rs/zerolog/log"
)

const (
	shardRateLimit      = 64 // Global limiter: maximum concurrent refreshes across all shards
	shardRateLimitBurst = 16 // Global limiter: maximum parallel requests.
	refreshSamples      = 16 // Number of items to sample per shard per refreshItem tick
)

var (
	refreshSuccessNumCh = make(chan struct{}, runtime.GOMAXPROCS(0)*4) // Successful refreshes counter channel
	refreshErroredNumCh = make(chan struct{}, runtime.GOMAXPROCS(0)*4) // Failed refreshes counter channel
)

type Refresher interface {
	Run()
}

// Refresh is responsible for background refreshing of cache entries.
// It periodically samples random shards and randomly selects "cold" entries
// (from the end of each shard's Storage list) to refreshItem if necessary.
type Refresh struct {
	ctx                context.Context
	cfg                *config.Cache
	balancer           lru.Balancer
	shardRateLimiter   *rate.Limiter
	refreshRateLimiter *rate.Limiter
}

// NewRefresher constructs a Refresh.
func NewRefresher(ctx context.Context, cfg *config.Cache, balancer lru.Balancer) *Refresh {
	return &Refresh{
		ctx:                ctx,
		cfg:                cfg,
		balancer:           balancer,
		shardRateLimiter:   rate.NewLimiter(ctx, shardRateLimit, shardRateLimitBurst),
		refreshRateLimiter: rate.NewLimiter(ctx, cfg.Cache.Refresh.Rate, cfg.Cache.Refresh.Rate/10),
	}
}

// Run starts the refresher background loop.
// It runs a logger (if debugging is enabled), spawns a provider for sampling shards,
// and continuously processes shard samples for candidate responses to refreshItem.
func (r *Refresh) Run() {
	go func() {
		r.runLogger()
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-r.shardRateLimiter.Chan(): // Throttling (64 per second)
				r.refreshNode(r.balancer.RandShardNode())
			}
		}
	}()
}

// refreshNode selects up to refreshSamples entries from the end of the given shard's Storage list.
// For each candidate, if ShouldBeRefreshed() returns true and shardRateLimiter limiting allows, triggers an asynchronous refreshItem.
func (r *Refresh) refreshNode(node *lru.ShardNode) {
	ctx, cancel := context.WithTimeout(r.ctx, time.Second)
	defer cancel()

	samples := 0
	node.Shard.Walk(ctx, func(u uint64, resp *model.Response) bool {
		if samples >= refreshSamples {
			return false
		}
		if resp.ShouldBeRefreshed() {
			select {
			case <-ctx.Done():
				return false
			case <-r.refreshRateLimiter.Chan(): // Throttling (1024 per second)
				go r.refreshItem(resp)
				samples++
			}
		}
		return true
	}, false)
}

// refreshItem attempts to refreshItem the given response via Revalidate.
// If successful, increments the refreshItem metric (in debug mode); otherwise increments the error metric.
func (r *Refresh) refreshItem(resp *model.Response) {
	if err := resp.Revalidate(r.ctx); err != nil {
		refreshErroredNumCh <- struct{}{}
		return
	}
	refreshSuccessNumCh <- struct{}{}
}

// runLogger periodically logs the number of successful and failed refreshItem attempts.
// This runs only if debugging is enabled in the config.
func (r *Refresh) runLogger() {
	go func() {
		erroredNumPer5Sec := 0
		refreshesNumPer5Sec := 0
		ticker := utils.NewTicker(r.ctx, 5*time.Second)

	loop:
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-refreshSuccessNumCh:
				refreshesNumPer5Sec++
			case <-refreshErroredNumCh:
				erroredNumPer5Sec++
			case <-ticker:
				if erroredNumPer5Sec <= 0 && refreshesNumPer5Sec <= 0 {
					continue loop
				}

				var (
					errorsNum  = strconv.Itoa(erroredNumPer5Sec)
					successNum = strconv.Itoa(refreshesNumPer5Sec)
				)

				logEvent := log.Info()

				if r.cfg.IsProd() {
					logEvent.
						Str("target", "refresher").
						Str("refreshes", successNum).
						Str("errors", errorsNum)
				}

				logEvent.Msgf("[refresher][5s] updated %s items, errors: %s", successNum, errorsNum)

				refreshesNumPer5Sec = 0
				erroredNumPer5Sec = 0
				runtime.Gosched()
			}
		}
	}()
}
