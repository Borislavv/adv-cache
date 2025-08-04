package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/header"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/pools"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	serverutils "github.com/Borislavv/advanced-cache/pkg/server/utils"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/fasthttp/router"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// CacheGetPath for getting pagedata from cache via HTTP.
const CacheGetPath = "/{any:*}"

// enabled indicates whether the advanced cache is turned on or off.
// It can be safely accessed and modified concurrently.
var enabled atomic.Bool

// Predefined HTTP response templates for error handling (400/503)
var (
	serviceUnavailableResponseBytes = []byte(`{
	  "status": 503,
	  "error": "Service Unavailable",
	  "message": "` + string(messagePlaceholder) + `"
	}`)
	messagePlaceholder = []byte("${message}")
)

var (
	total         = &atomic.Int64{}
	hits          = &atomic.Int64{}
	misses        = &atomic.Int64{}
	proxies       = &atomic.Int64{}
	errors        = &atomic.Int64{}
	totalDuration = &atomic.Int64{} // UnixNano
)

// CacheController handles cache API requests (read/write-through, error reporting, metrics).
type CacheController struct {
	cfg      *config.Cache
	ctx      context.Context
	cache    storage.Storage
	metrics  metrics.Meter
	backend  repository.Backender
	errorsCh chan error
}

// NewCacheController builds a cache API controller with all dependencies.
// If debug is enabled, launches internal stats logger goroutine.
func NewCacheController(
	ctx context.Context,
	cfg *config.Cache,
	cache storage.Storage,
	metrics metrics.Meter,
	backend repository.Backender,
) *CacheController {
	c := &CacheController{
		cfg:      cfg,
		ctx:      ctx,
		cache:    cache,
		metrics:  metrics,
		backend:  backend,
		errorsCh: make(chan error, 8196),
	}
	enabled.Store(cfg.Cache.Enabled)
	c.runLoggerMetricsWriter()
	c.runErrorLogger()
	return c
}

// Index is the main HTTP handler.
func (c *CacheController) Index(r *fasthttp.RequestCtx) {
	var from = time.Now()
	defer func() { totalDuration.Add(time.Since(from).Nanoseconds()) }()

	total.Add(1)
	if enabled.Load() {
		c.handleTroughCache(r)
	} else {
		c.handleTroughProxy(r)
	}
}

func (c *CacheController) handleTroughCache(r *fasthttp.RequestCtx) {
	// make a lightweight request Entry (contains only key, shardKey and fingerprint)
	newEntry, err := model.NewEntryFastHttp(c.cfg, r) // must be removed on hit and release on miss
	if err != nil {
		if model.IsRouteWasNotFound(err) {
			c.handleTroughProxy(r)
			return
		}
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	var (
		payloadStatus       int
		payloadHeaders      *[][2][]byte
		payloadBody         []byte
		payloadLastModified int64
	)

	foundEntry, found := c.cache.Get(newEntry)
	if !found {
		misses.Add(1)

		// extract request data
		path := r.Path()
		rule := newEntry.Rule()
		queryString := r.QueryArgs().QueryString()
		queryHeaders, queryReleaser := c.queryHeaders(r)
		defer queryReleaser(queryHeaders)

		// fetch data from upstream
		var payloadReleaser func()
		payloadStatus, payloadHeaders, payloadBody, payloadReleaser, err = c.backend.Fetch(rule, path, queryString, queryHeaders)
		defer payloadReleaser()
		if err != nil {
			errors.Add(1)
			c.respondThatServiceIsTemporaryUnavailable(err, r)
			return
		}

		if payloadStatus != http.StatusOK {
			// bad status code received, process request and don't store in cache, removed after use of course
			payloadLastModified = time.Now().UnixNano()
		} else {
			newEntry.SetPayload(path, queryString, queryHeaders, payloadHeaders, payloadBody, payloadStatus)
			newEntry.SetRevalidator(c.backend.RevalidatorMaker())

			c.cache.Set(newEntry)

			payloadLastModified = newEntry.UpdateAt()
		}
	} else {
		hits.Add(1)

		payloadLastModified = foundEntry.UpdateAt()

		// unpack found Entry data
		var queryHeaders *[][2][]byte
		var payloadReleaser func(q *[][2][]byte, h *[][2][]byte)
		_, _, queryHeaders, payloadHeaders, payloadBody, payloadStatus, payloadReleaser, err = foundEntry.Payload()
		defer payloadReleaser(queryHeaders, payloadHeaders)
		if err != nil {
			c.respondThatServiceIsTemporaryUnavailable(err, r)
			return
		}
	}

	// Write payloadStatus, payloadHeaders, and payloadBody from the cached (or fetched) response.
	r.Response.SetStatusCode(payloadStatus)
	for _, kv := range *payloadHeaders {
		r.Response.Header.AddBytesKV(kv[0], kv[1])
	}

	// Set up Last-Modified header
	header.SetLastModifiedValueFastHttp(r, payloadLastModified)

	// Write payloadBody
	if _, err = serverutils.Write(payloadBody, r); err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}
}

func (c *CacheController) handleTroughProxy(r *fasthttp.RequestCtx) {
	proxies.Add(1)

	// extract request data
	path := r.Path()
	queryString := r.QueryArgs().QueryString()
	queryHeaders, queryReleaser := c.queryHeaders(r)
	defer queryReleaser(queryHeaders)

	// fetch data from upstream
	payloadStatus, payloadHeaders, payloadBody, payloadReleaser, err := c.backend.Fetch(nil, path, queryString, queryHeaders)
	defer payloadReleaser()
	if err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	// Write payloadStatus, payloadHeaders, and payloadBody from the cached (or fetched) response.
	r.Response.SetStatusCode(payloadStatus)
	for _, kv := range *payloadHeaders {
		r.Response.Header.AddBytesKV(kv[0], kv[1])
	}

	// Set up Last-Modified header
	header.SetLastModifiedValueFastHttp(r, time.Now().UnixNano())

	// Write payloadBody
	if _, err = serverutils.Write(payloadBody, r); err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}
}

var contentType = []byte("application/json")

// respondThatServiceIsTemporaryUnavailable returns 503 and logs the error.
func (c *CacheController) respondThatServiceIsTemporaryUnavailable(err error, ctx *fasthttp.RequestCtx) {
	c.errorsCh <- err
	ctx.Response.Header.SetContentTypeBytes(contentType)
	ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
	if _, err = serverutils.Write(c.resolveMessagePlaceholder(serviceUnavailableResponseBytes, err), ctx); err != nil {
		c.errorsCh <- err
	}
}

// resolveMessagePlaceholder substitutes ${message} in template with escaped error message.
func (c *CacheController) resolveMessagePlaceholder(msg []byte, err error) []byte {
	escaped, _ := json.Marshal(err.Error())
	return bytes.ReplaceAll(msg, messagePlaceholder, escaped[1:len(escaped)-1])
}

// AddRoute attaches controller's route(s) to the provided router.
func (c *CacheController) AddRoute(router *router.Router) {
	router.GET(CacheGetPath, c.Index)
}

var (
	// if you return a releaser as an outer variable it will not allocate closure each time on call function
	queryHeadersReleaser = func(headers *[][2][]byte) {
		*headers = (*headers)[:0]
		pools.KeyValueSlicePool.Put(headers)
	}
)

func (c *CacheController) queryHeaders(r *fasthttp.RequestCtx) (headers *[][2][]byte, releaseFn func(*[][2][]byte)) {
	headers = pools.KeyValueSlicePool.Get().(*[][2][]byte)
	r.Request.Header.All()(func(key []byte, value []byte) bool {
		*headers = append(*headers, [2][]byte{key, value})
		return true
	})
	return headers, queryHeadersReleaser
}

func (c *CacheController) runErrorLogger() {
	go func() {
		var prev map[string]int
		dedupMap := make(map[string]int, 2048)
		each5Secs := utils.NewTicker(c.ctx, time.Second*5)

		writeTrigger := make(chan struct{}, 1)
		go func() {
			for range writeTrigger {
				for err, count := range prev {
					log.Error().Msgf("[error-logger] %s (count=%d)", err, count)
				}
			}
		}()

		for {
			select {
			case <-c.ctx.Done():
				return
			case err := <-c.errorsCh:
				dedupMap[err.Error()]++
			case <-each5Secs:
				prev = dedupMap
				dedupMap = make(map[string]int, len(prev))
				writeTrigger <- struct{}{}
			}
		}
	}()
}

func (c *CacheController) runLoggerMetricsWriter() {
	go func() {
		metricsTicker := utils.NewTicker(c.ctx, time.Second)

		var (
			// 5s логика
			totalNum         int64
			hitsNum          int64
			missesNum        int64
			errorsNum        int64
			proxiedNum       int64
			totalDurationNum int64

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
		prev := time.Now()

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

				proxiedNumLoc := proxies.Load()
				proxies.Store(0)

				errorsNumLoc := errors.Load()
				errors.Store(0)

				totalDurationNumLoc := totalDuration.Load()
				totalDuration.Store(0)

				// metrics export
				var avgDuration float64
				if totalNumLoc > 0 {
					avgDuration = float64(totalDurationNumLoc) / float64(totalNumLoc)
				}
				memUsage, length := c.cache.Stat()
				c.metrics.SetCacheLength(uint64(length))
				c.metrics.SetCacheMemory(uint64(memUsage))
				c.metrics.SetHits(uint64(hitsNumLoc))
				c.metrics.SetMisses(uint64(missesNumLoc))
				c.metrics.SetErrors(uint64(errorsNumLoc))
				c.metrics.SetProxiedNum(uint64(proxiedNumLoc))
				c.metrics.SetRPS(float64(totalNumLoc))
				c.metrics.SetAvgResponseTime(avgDuration)

				totalNum += totalNumLoc
				hitsNum += hitsNumLoc
				missesNum += missesNumLoc
				errorsNum += errorsNumLoc
				proxiedNum += proxiedNumLoc
				totalDurationNum += totalDurationNumLoc

				accHourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, proxiedNumLoc, totalDurationNumLoc)
				acc12Hourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, proxiedNumLoc, totalDurationNumLoc)
				acc24Hourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, proxiedNumLoc, totalDurationNumLoc)

				if i == logIntervalSecs {
					elapsed := time.Since(prev)
					duration := time.Duration(int(avgDuration))
					rps := float64(totalNum) / elapsed.Seconds()

					if duration == 0 && rps == 0 {
						continue
					}

					logEvent := log.Info()
					var target string
					if enabled.Load() {
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
							Str("avgDuration", duration.String()).
							Str("elapsed", elapsed.String())
					}

					if enabled.Load() {
						logEvent.Msgf(
							"[%s][%s] served %d requests (rps: %.f, avg.dur.: %s hits: %d, misses: %d, errors: %d)",
							target, elapsed.String(), totalNum, rps, duration.String(), hitsNum, missesNum, errorsNum,
						)
					} else {
						logEvent.Msgf(
							"[%s][%s] served %d requests (rps: %.f, avg.dur.: %s total: %d, proxied: %d, errors: %d)",
							target, elapsed.String(), totalNum, rps, duration.String(), totalNum, proxiedNum, errorsNum,
						)
					}

					totalNum = 0
					hitsNum = 0
					missesNum = 0
					errorsNum = 0
					proxiedNum = 0
					totalDurationNum = 0
					prev = time.Now()
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
	total    int64
	hits     int64
	misses   int64
	errors   int64
	proxied  int64
	duration int64
}

func (c *counters) add(total, hits, misses, errors, proxied, dur int64) {
	c.total += total
	c.hits += hits
	c.misses += misses
	c.errors += errors
	c.proxied += proxied
	c.duration += dur
}

func (c *counters) reset() {
	c.total, c.hits, c.misses, c.errors, c.proxied, c.duration = 0, 0, 0, 0, 0, 0
}

func logLong(label string, c counters, cfg *config.Cache) {
	if c.total == 0 {
		return
	}

	var (
		avgDur = time.Duration(0)
		avgRPS float64
	)

	if c.total > 0 {
		avgDur = time.Duration(int(c.duration / c.total))

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
			Int64("errors", c.errors).
			Int64("proxied", c.proxied).
			Float64("avgRPS", avgRPS).
			Str("avgDuration", avgDur.String())
	}

	logEvent.Msgf("[cache][%s] total=%d hits=%d misses=%d errors=%d proxied=%d avgRPS=%.2f avgDur=%s",
		label, c.total, c.hits, c.misses, c.errors, c.proxied, avgRPS, avgDur.String())
}
