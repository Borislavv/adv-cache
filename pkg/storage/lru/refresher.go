package lru

import (
	"context"
	"errors"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/rate"
	"github.com/Borislavv/advanced-cache/pkg/upstream"
	"net/http"
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

var ErrRefreshUpstreamBadStatusCode = errors.New("invalid upstream status code")

type Refresher interface {
	Run()
}

// Refresh is responsible for background refreshing of cache entries.
// It periodically samples random shards and randomly selects "cold" entries
// (from the end of each shard's InMemoryStorage list) to refreshItem if necessary.
// Communication: provider->consumer (MPSC).
type Refresh struct {
	ctx      context.Context
	cfg      config.Config
	storage  Storage
	upstream upstream.Upstream
	errorsCh chan error
}

// NewRefresher constructs a Refresh.
func NewRefresher(ctx context.Context, cfg config.Config, storage Storage, upstream upstream.Upstream) *Refresh {
	return &Refresh{
		ctx:      ctx,
		cfg:      cfg,
		storage:  storage,
		upstream: upstream,
		errorsCh: make(chan error, 2048),
	}
}

// Run starts the refresher background loop.
// It runs a logger (if debugging is enabled), spawns a provider for sampling shards,
// and continuously processes shard samples for candidate responses to refreshItem.
func (r *Refresh) Run() {
	if r.cfg.IsEnabled() && r.cfg.Refresh().Enabled {
		r.runLogger()           // handle consumer stats and print logs
		r.runDedupErrorLogger() // deduplicates errors
		r.runInstances()        // runInstances workers (N=workersNum) which scan the storage and runInstances async refresh tasks
	}
}

func (r *Refresh) runInstances() {
	scanRateCh := rate.NewLimiter(r.ctx, r.cfg.Refresh().ScanRate, r.cfg.Refresh().ScanRate/10).Chan()
	upstreamRateCh := rate.NewLimiter(r.ctx, r.cfg.Refresh().Rate, r.cfg.Refresh().Rate/10).Chan()

	for id := 0; id < workersNum; id++ {
		go func(id int) {
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
							go func(refresherID int) {
								if err := r.refresh(entry); err != nil {
									r.errorsCh <- err
									failedRefreshesNumCounter.Add(1)
								} else {
									successRefreshesNumCounter.Add(1)
								}
							}(id)
						}
					}
				}
			}
		}(id)
	}
}

func (r *Refresh) refresh(e *model.Entry) error {
	req, resp, releaser, err := e.Payload()
	defer releaser(req, resp)
	if err != nil {
		return err
	}

	req, resp, releaser, err = r.upstream.Fetch(e.Rule(), nil, req)
	defer releaser(req, resp)
	if err != nil {
		return err
	}

	if resp.StatusCode() != http.StatusOK {
		return ErrRefreshUpstreamBadStatusCode
	} else {
		e.SetPayload(req, resp)
	}

	return nil
}

func (r *Refresh) runDedupErrorLogger() {
	go func() {
		var (
			prev = atomic.Pointer[map[string]int]{}
			cur  = atomic.Pointer[map[string]int]{}
		)
		curMap := make(map[string]int, 48)
		cur.Store(&curMap)

		each5Secs := utils.NewTicker(r.ctx, time.Second*5)
		writeTrigger := make(chan struct{}, 1)
		defer close(writeTrigger)

		go func() {
			for range writeTrigger {
				for err, count := range *prev.Load() {
					log.Error().Msgf("[refresher][error] %s (count=%d)", err, count)
				}
			}
		}()

		for {
			select {
			case <-r.ctx.Done():
				return
			case err := <-r.errorsCh:
				(*cur.Load())[err.Error()]++
			case <-each5Secs:
				prev.Store(cur.Load())
				newMap := make(map[string]int, len(*prev.Load()))
				cur.Store(&newMap)
				writeTrigger <- struct{}{}
			}
		}
	}()
}

// runLogger periodically logs the number of successful and failed refreshItem attempts.
// This runs only if debugging is enabled in the config.
func (r *Refresh) runLogger() {
	go func() {
		each5Secs := utils.NewTicker(r.ctx, 5*time.Second)
		eachHour := utils.NewTicker(r.ctx, time.Hour)
		<-eachHour
		each12Hours := utils.NewTicker(r.ctx, 12*time.Hour)
		<-each12Hours
		each24Hours := utils.NewTicker(r.ctx, 24*time.Hour)
		<-each24Hours

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
			logEvent.Msgf("[refresher][%s] refreshes: %d, errors: %d, scans: %d, found: %d",
				label, c.success, c.errors, c.scans, c.found)
		}

		for {
			select {
			case <-r.ctx.Done():
				return

			case <-each5Secs:
				success := successRefreshesNumCounter.Swap(0)
				errs := failedRefreshesNumCounter.Swap(0)
				scans := scansNumCounter.Swap(0)
				found := scansFoundNumCounter.Swap(0)

				accHourly.success += success
				accHourly.errors += errs
				accHourly.scans += scans
				accHourly.found += found

				acc12Hourly.success += success
				acc12Hourly.errors += errs
				acc12Hourly.scans += scans
				acc12Hourly.found += found

				acc24Hourly.success += success
				acc24Hourly.errors += errs
				acc24Hourly.scans += scans
				acc24Hourly.found += found

				logEvent := log.Info()
				if r.cfg.IsProd() {
					logEvent = logEvent.
						Str("target", "refresher").
						Int64("refreshes", success).
						Int64("errors", errs).
						Int64("scans", scans).
						Int64("scans_found", found)
				}
				logEvent.Msgf("[refresher][5s] refreshes: %d, errors: %d, scans: %d, found: %d",
					success, errs, scans, found)

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
