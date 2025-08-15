package api

import (
	"context"
	"errors"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/ctime"
	"github.com/Borislavv/advanced-cache/pkg/http/responder"
	"github.com/Borislavv/advanced-cache/pkg/http/template"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/upstream"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/fasthttp/router"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"net/http"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

// CacheGetPath for getting pagedata from cache via HTTP.
const CacheGetPath = "/{any:*}"

var (
	total   = &atomic.Int64{}
	hits    = &atomic.Int64{}
	misses  = &atomic.Int64{}
	proxied = &atomic.Int64{}
	errered = &atomic.Int64{}
)

var (
	contentType              = []byte("application/json")
	errNeedRetryThroughProxy = errors.New("need retry through proxy")
	bufCap                   = runtime.GOMAXPROCS(0) * 4
)

// CacheProxyController handles cache API requests (read/write-through, error reporting, metrics).
type CacheProxyController struct {
	cfg      config.Config
	ctx      context.Context
	cache    storage.Storage
	metrics  metrics.Meter
	upstream upstream.Upstream
	errorsCh chan error
}

// NewCacheProxyController builds a cache API controller with all dependencies.
// If debug is enabled, launches internal stats logger goroutine.
func NewCacheProxyController(
	ctx context.Context,
	cfg config.Config,
	cache storage.Storage,
	metrics metrics.Meter,
	backend upstream.Upstream,
) *CacheProxyController {
	c := &CacheProxyController{
		cfg:      cfg,
		ctx:      ctx,
		cache:    cache,
		metrics:  metrics,
		upstream: backend,
		errorsCh: make(chan error, bufCap),
	}
	c.runErrorLogger()
	c.runLoggerMetricsWriter()
	return c
}

// AddRoute attaches controller's route(s) to the provided router.
func (c *CacheProxyController) AddRoute(router *router.Router) {
	router.GET(CacheGetPath, c.Index)
}

// Index is the main HTTP handler.
func (c *CacheProxyController) Index(ctx *fasthttp.RequestCtx) {
	total.Add(1)
	if c.cfg.IsEnabled() {
		if err := c.handleTroughCache(ctx); err != nil {
			if !errors.Is(err, errNeedRetryThroughProxy) {
				c.respondServiceIsUnavailable(ctx, err)
				errered.Add(1)
				return
			}
		} else {
			return
		}
	}

	proxied.Add(1)
	if err := c.handleTroughProxy(ctx); err != nil {
		c.respondServiceIsUnavailable(ctx, err)
	}
}

func (c *CacheProxyController) handleTroughCache(ctx *fasthttp.RequestCtx) error {
	// make a lightweight requestedEntry Entry (contains only key, shardKey and fingerprint)
	requestedEntry, err := model.NewEntry(c.cfg, ctx)
	if err != nil {
		// retry though proxy if cache path was not found
		return errNeedRetryThroughProxy
	}

	var (
		found bool
		entry *model.Entry
	)

	if entry, found = c.cache.Get(requestedEntry); found {
		hits.Add(1)
		// write found response and return it
		if err = responder.WriteFromEntry(ctx, entry); err != nil {
			c.logError(err)
			// retry though proxy if payload unpack has errors
			return errNeedRetryThroughProxy
		}
	} else {
		misses.Add(1)
		// fetch missed data from upstream
		req, resp, releaser, ferr := c.upstream.Fetch(requestedEntry.Rule(), ctx, nil)
		defer releaser(req, resp)
		if ferr != nil {
			c.logError(ferr)
			return ferr
		}
		// store fetched data only if it's positive
		if resp.StatusCode() == http.StatusOK {
			requestedEntry.SetPayload(req, resp)
			// persist new entry (make it available other threads)
			c.cache.Set(requestedEntry)
			// set the new entry as found
			entry = requestedEntry
		}
		// write fetched response and return it
		responder.WriteFromResponse(ctx, resp, entry.UpdatedAt())
	}

	return nil
}

func (c *CacheProxyController) handleTroughProxy(ctx *fasthttp.RequestCtx) error {
	// fetch missed data from upstream
	req, resp, releaser, err := c.upstream.Fetch(nil, ctx, nil)
	defer releaser(req, resp)
	if err != nil {
		c.logError(err)
		return err
	}
	// write fetched response and return it
	responder.WriteFromResponse(ctx, resp, 0)
	return nil
}

// respondServiceIsUnavailable returns 503 and logs the error.
func (c *CacheProxyController) respondServiceIsUnavailable(ctx *fasthttp.RequestCtx, err error) {
	c.logError(err)
	ctx.Response.Header.SetContentTypeBytes(contentType)
	ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
	if _, err = ctx.Write(template.RespondUnavailable(err)); err != nil {
		c.logError(err)
	}
}

func (c *CacheProxyController) logError(err error) {
	select {
	case <-c.ctx.Done():
	case c.errorsCh <- err:
	default:
	}
}

func (c *CacheProxyController) runErrorLogger() {
	go func() {
		var (
			prev = atomic.Pointer[map[string]int]{}
			cur  = atomic.Pointer[map[string]int]{}
		)
		curMap := make(map[string]int, 48)
		cur.Store(&curMap)

		each5Secs := utils.NewTicker(c.ctx, time.Second*5)
		writeTrigger := make(chan struct{}, 1)
		defer close(writeTrigger)

		go func() {
			for range writeTrigger {
				for err, count := range *prev.Load() {
					log.Error().Msgf("[dedup-err-logger][5s] %s (count=%d)", err, count)
				}
			}
		}()

		for {
			select {
			case <-c.ctx.Done():
				return
			case err := <-c.errorsCh:
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

func (c *CacheProxyController) runLoggerMetricsWriter() {
	go func() {
		metricsTicker := utils.NewTicker(c.ctx, time.Second)

		var (
			// 5s window
			totalNum   int64
			hitsNum    int64
			missesNum  int64
			errorsNum  int64
			proxiedNum int64

			accHourly   counters
			acc12Hourly counters
			acc24Hourly counters

			// тикеры
			eachHour   = time.NewTicker(time.Hour)
			each12Hour = time.NewTicker(12 * time.Hour)
			each24Hour = time.NewTicker(24 * time.Hour)
		)

		const logIntervalSecs = 5
		i := logIntervalSecs
		prev := ctime.Now()

		for {
			select {
			case <-c.ctx.Done():
				return

			case <-metricsTicker:
				totalNumLoc := total.Load()
				total.Store(0)

				hitsNumLoc := hits.Load()
				hits.Store(0)

				missesNumLoc := misses.Load()
				misses.Store(0)

				proxiedNumLoc := proxied.Load()
				proxied.Store(0)

				errorsNumLoc := errered.Load()
				errered.Store(0)

				// metrics export
				memUsage, length := c.cache.Stat()
				c.metrics.SetCacheLength(uint64(length))
				c.metrics.SetCacheMemory(uint64(memUsage))
				c.metrics.SetHits(uint64(hitsNumLoc))
				c.metrics.SetMisses(uint64(missesNumLoc))
				c.metrics.SetTotal(uint64(totalNumLoc))
				c.metrics.SetErrors(uint64(errorsNumLoc))
				c.metrics.SetProxiedNum(uint64(proxiedNumLoc))
				c.metrics.SetRPS(float64(totalNumLoc))

				totalNum += totalNumLoc
				hitsNum += hitsNumLoc
				missesNum += missesNumLoc
				errorsNum += errorsNumLoc
				proxiedNum += proxiedNumLoc

				accHourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, proxiedNumLoc)
				acc12Hourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, proxiedNumLoc)
				acc24Hourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, proxiedNumLoc)

				if i == logIntervalSecs {
					elapsed := time.Since(prev)
					rps := float64(totalNum) / elapsed.Seconds()

					if rps == 0 {
						continue
					}

					logEvent := log.Info()
					var target string
					if c.cfg.IsEnabled() {
						target = "cache-controller"
					} else {
						target = "proxy-controller"
					}

					if c.cfg.IsProd() {
						logEvent.
							Str("target", target).
							Str("rps", strconv.Itoa(int(rps))).
							Str("served", strconv.Itoa(int(totalNum))).
							Str("periodMs", strconv.Itoa(logIntervalSecs*1000)).
							Str("elapsed", elapsed.String())
					}

					if c.cfg.IsEnabled() {
						logEvent.Msgf(
							"[%s][5s] served %d requests (rps: %.f, hits: %d, misses: %d, errered: %d)",
							target, totalNum, rps, hitsNum, missesNum, errorsNum,
						)
					} else {
						logEvent.Msgf(
							"[%s][5s] served %d requests (rps: %.f, total: %d, proxied: %d, errered: %d)",
							target, totalNum, rps, totalNum, proxiedNum, errorsNum,
						)
					}

					totalNum = 0
					hitsNum = 0
					missesNum = 0
					errorsNum = 0
					proxiedNum = 0
					prev = ctime.Now()
					i = 0
				}
				i++

			case <-eachHour.C:
				logLong("1h", accHourly, c.cfg)
				accHourly.reset()

			case <-each12Hour.C:
				logLong("12h", acc12Hourly, c.cfg)
				acc12Hourly.reset()

			case <-each24Hour.C:
				logLong("24h", acc24Hourly, c.cfg)
				acc24Hourly.reset()
			}
		}
	}()
}

type counters struct {
	total   int64
	hits    int64
	misses  int64
	errors  int64
	proxied int64
}

func (c *counters) add(total, hits, misses, errors, proxied int64) {
	c.total += total
	c.hits += hits
	c.misses += misses
	c.errors += errors
	c.proxied += proxied
}

func (c *counters) reset() {
	c.total, c.hits, c.misses, c.errors, c.proxied = 0, 0, 0, 0, 0
}

func logLong(label string, c counters, cfg config.Config) {
	if c.total == 0 {
		return
	}

	var avgRPS float64
	if c.total > 0 {
		switch label {
		case "1h":
			avgRPS = float64(c.total) / 3600
		case "12h":
			avgRPS = float64(c.total) / (12 * 3600)
		case "24h":
			avgRPS = float64(c.total) / (24 * 3600)
		}
	}

	logEvent := log.Info()
	if cfg.IsProd() {
		logEvent = logEvent.
			Str("target", "cache-long-metrics").
			Str("period", label).
			Int64("total", c.total).
			Int64("hits", c.hits).
			Int64("misses", c.misses).
			Int64("errered", c.errors).
			Int64("proxied", c.proxied).
			Float64("avgRPS", avgRPS)
	}

	logEvent.Msgf("[cache][%s] total=%d hits=%d misses=%d errered=%d proxied=%d avgRPS=%.2f",
		label, c.total, c.hits, c.misses, c.errors, c.proxied, avgRPS)
}
