package lru

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/rate"
	"sync/atomic"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/rs/zerolog/log"
)

const workersNum = 4

var (
	scansNumCounter            = atomic.Int64{}
	scansFoundNumCounter       = atomic.Int64{}
	successRefreshesNumCounter = atomic.Int64{}
	failedRefreshesNumCounter  = atomic.Int64{}
)

type Refresher interface {
	run()
}

// Refresh is responsible for background refreshing of cache entries.
// It periodically samples random shards and randomly selects "cold" entries
// (from the end of each shard's InMemoryStorage list) to refreshItem if necessary.
// Communication: provider->consumer (MPSC).
type Refresh struct {
	ctx     context.Context
	cfg     *config.Cache
	storage *InMemoryStorage
}

// NewRefresher constructs a Refresh.
func NewRefresher(ctx context.Context, cfg *config.Cache, storage *InMemoryStorage) *Refresh {
	return &Refresh{
		ctx:     ctx,
		cfg:     cfg,
		storage: storage,
	}
}

// Run starts the refresher background loop.
// It runs a logger (if debugging is enabled), spawns a provider for sampling shards,
// and continuously processes shard samples for candidate responses to refreshItem.
func (r *Refresh) Run() *Refresh {
	if r.cfg.Cache.Enabled && r.cfg.Cache.Refresh.Enabled {
		r.runLogger() // handle consumer stats and print logs
		r.run()       // run workers (N=workersNum) which scan the storage and run async refresh tasks
	}
	return r
}

func (r *Refresh) run() {
	scanRateCh := rate.NewLimiter(r.ctx, r.cfg.Cache.Refresh.ScanRate, r.cfg.Cache.Refresh.ScanRate/10).Chan()
	upstreamRateCh := rate.NewLimiter(r.ctx, r.cfg.Cache.Refresh.Rate, r.cfg.Cache.Refresh.Rate/10).Chan()

	for i := 0; i < workersNum; i++ {
		go func() {
			for {
				select {
				case <-r.ctx.Done():
					return
				case <-scanRateCh:
					scansNumCounter.Add(1)
					if entry, found := r.storage.Rand(); found && entry.ShouldBeRefreshed(r.cfg) {
						scansFoundNumCounter.Add(1)
						select {
						case <-r.ctx.Done():
							return
						case <-upstreamRateCh:
							go func() {
								if err := entry.Revalidate(); err != nil {
									failedRefreshesNumCounter.Add(1)
								} else {
									successRefreshesNumCounter.Add(1)
								}
							}()
						}
					}
				}
			}
		}()
	}
}

// runLogger periodically logs the number of successful and failed refreshItem attempts.
// This runs only if debugging is enabled in the config.
func (r *Refresh) runLogger() {
	go func() {
		each5Secs := utils.NewTicker(r.ctx, 5*time.Second)
		eachHour := utils.NewTicker(r.ctx, time.Hour)
		each12Hours := utils.NewTicker(r.ctx, 12*time.Hour)
		each24Hours := utils.NewTicker(r.ctx, 24*time.Hour)

		type counters struct {
			success int64
			errors  int64
			scans   int64
			found   int64
		}

		var (
			accHourly   = &counters{}
			acc12Hourly = &counters{}
			acc24Hourly = &counters{}
		)

		reset := func(c *counters) {
			c.success, c.errors, c.scans, c.found = 0, 0, 0, 0
		}

		logCounters := func(label string, c *counters) {
			logEvent := log.Info()
			if r.cfg.IsProd() {
				logEvent = logEvent.
					Str("target", "refresher").
					Int64("refreshes", c.success).
					Int64("errors", c.errors).
					Int64("scans", c.scans).
					Int64("scans_found", c.found)
			}
			logEvent.Msgf("[refresher][%s] refreshes=%d, errors=%d, scans=%d, found=%d",
				label, c.success, c.errors, c.scans, c.found)
		}

		for {
			select {
			case <-r.ctx.Done():
				return

			case <-each5Secs:
				success := successRefreshesNumCounter.Swap(0)
				errors := failedRefreshesNumCounter.Swap(0)
				scans := scansNumCounter.Swap(0)
				found := scansFoundNumCounter.Swap(0)

				accHourly.success += success
				accHourly.errors += errors
				accHourly.scans += scans
				accHourly.found += found

				acc12Hourly.success += success
				acc12Hourly.errors += errors
				acc12Hourly.scans += scans
				acc12Hourly.found += found

				acc24Hourly.success += success
				acc24Hourly.errors += errors
				acc24Hourly.scans += scans
				acc24Hourly.found += found

				logEvent := log.Info()
				if r.cfg.IsProd() {
					logEvent = logEvent.
						Str("target", "refresher").
						Int64("refreshes", success).
						Int64("errors", errors).
						Int64("scans", scans).
						Int64("scans_found", found)
				}
				logEvent.Msgf("[refresher][5s] refreshes=%d, errors=%d, scans=%d, found=%d",
					success, errors, scans, found)

			case <-eachHour:
				logCounters("1h", accHourly)
				reset(accHourly)

			case <-each12Hours:
				logCounters("12h", acc12Hourly)
				reset(acc12Hourly)

			case <-each24Hours:
				logCounters("24h", acc24Hourly)
				reset(acc24Hourly)
			}
		}
	}()
}
