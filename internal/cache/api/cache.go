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
	hits          = &atomic.Uint64{}
	misses        = &atomic.Uint64{}
	errors        = &atomic.Uint64{}
	totalDuration = &atomic.Int64{} // UnixNano
)

// CacheController handles cache API requests (read/write-through, error reporting, metrics).
type CacheController struct {
	cfg     *config.Cache
	ctx     context.Context
	cache   storage.Storage
	metrics metrics.Meter
	backend repository.Backender
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
		cfg:     cfg,
		ctx:     ctx,
		cache:   cache,
		metrics: metrics,
		backend: backend,
	}
	enabled.Store(cfg.Cache.Enabled)
	c.runLoggerMetricsWriter()
	return c
}

// Index is the main HTTP handler.
func (c *CacheController) Index(r *fasthttp.RequestCtx) {
	if enabled.Load() {
		c.handleTroughCache(r)
	} else {
		c.handleTroughProxy(r)
	}
}

func (c *CacheController) handleTroughProxy(r *fasthttp.RequestCtx) {
	var from = time.Now()
	defer func() { totalDuration.Add(time.Since(from).Nanoseconds()) }()

	hits.Add(1)

	// make a lightweight request Entry (contains only key, shardKey and fingerprint)
	newEntry, err := model.NewEntryFastHttp(c.cfg, r) // must be removed on hit and release on miss
	defer newEntry.Remove()
	if err != nil {
		errors.Add(1)
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	// extract request data
	path := r.Path()
	rule := newEntry.Rule()
	queryString := r.QueryArgs().QueryString()
	queryHeaders, queryReleaser := c.queryHeaders(r)
	defer queryReleaser(queryHeaders)

	// fetch data from upstream
	payloadStatus, payloadHeaders, payloadBody, payloadReleaser, err := c.backend.Fetch(rule, path, queryString, queryHeaders)
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

	// Push up Last-Modified header
	header.SetLastModifiedValueFastHttp(r, time.Now().UnixNano())

	// Write payloadBody
	if _, err = serverutils.Write(payloadBody, r); err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}
}

func (c *CacheController) handleTroughCache(r *fasthttp.RequestCtx) {
	var from = time.Now()
	defer func() { totalDuration.Add(time.Since(from).Nanoseconds()) }()

	// make a lightweight request Entry (contains only key, shardKey and fingerprint)
	newEntry, err := model.NewEntryFastHttp(c.cfg, r) // must be removed on hit and release on miss
	if err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	var (
		payloadStatus       int
		payloadHeaders      *[][2][]byte
		payloadBody         []byte
		payloadLastModified int64
	)

	cacheEntry, found := c.cache.Get(newEntry)
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
			defer newEntry.Remove()

			payloadLastModified = time.Now().UnixNano()
		} else {
			newEntry.SetPayload(path, queryString, queryHeaders, payloadHeaders, payloadBody, payloadStatus)
			newEntry.SetRevalidator(c.backend.RevalidatorMaker())

			// build and store new VersionPointer in cache
			var wasPersisted bool
			cacheEntry, wasPersisted = c.cache.Set(model.NewVersionPointer(newEntry))
			if wasPersisted {
				defer cacheEntry.Release() // an Entry stored in the cache must be released after use
			} else {
				defer cacheEntry.Remove() // an Entry was not persisted, must be removed after use
			}

			payloadLastModified = cacheEntry.UpdateAt()
		}
	} else {
		hits.Add(1)

		// deferred release and remove
		newEntry.Remove()          // new Entry which was used as request for query cache does not need anymore
		defer cacheEntry.Release() // an Entry retrieved from the cache must be released after use

		// unpack found Entry data
		var queryHeaders *[][2][]byte
		var payloadReleaser func(q *[][2][]byte, h *[][2][]byte)
		_, _, queryHeaders, payloadHeaders, payloadBody, payloadStatus, payloadReleaser, err = cacheEntry.Payload()
		defer payloadReleaser(queryHeaders, payloadHeaders)
		if err != nil {
			c.respondThatServiceIsTemporaryUnavailable(err, r)
			return
		}

		payloadLastModified = cacheEntry.UpdateAt()
	}

	// Write payloadStatus, payloadHeaders, and payloadBody from the cached (or fetched) response.
	r.Response.SetStatusCode(payloadStatus)
	for _, kv := range *payloadHeaders {
		r.Response.Header.AddBytesKV(kv[0], kv[1])
	}

	// Push up Last-Modified header
	header.SetLastModifiedValueFastHttp(r, payloadLastModified)

	// Write payloadBody
	if _, err = serverutils.Write(payloadBody, r); err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}
}

// respondThatServiceIsTemporaryUnavailable returns 503 and logs the error.
func (c *CacheController) respondThatServiceIsTemporaryUnavailable(err error, ctx *fasthttp.RequestCtx) {
	log.Error().Err(err).Msg("[cache-controller] handle request error") // Don't move it down due to error will be rewritten.

	ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
	if _, err = serverutils.Write(c.resolveMessagePlaceholder(serviceUnavailableResponseBytes, err), ctx); err != nil {
		log.Err(err).Msg("failed to write into *fasthttp.RequestCtx")
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

func (c *CacheController) runLoggerMetricsWriter() {
	go func() {
		metricsTicker := utils.NewTicker(c.ctx, time.Second)

		var (
			totalNum         uint64
			hitsNum          uint64
			missesNum        uint64
			errorsNum        uint64
			totalDurationNum int64
		)

		const logIntervalSecs = 5
		i := logIntervalSecs
		prev := time.Now()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-metricsTicker:
				hitsNumLoc := hits.Load()
				missesNumLoc := misses.Load()
				errorsNumLoc := errors.Load()
				totalNumLoc := hitsNumLoc + missesNumLoc
				totalDurationNumLoc := totalDuration.Load()

				var avgDuration float64
				if totalNumLoc > 0 {
					avgDuration = float64(totalDurationNumLoc) / float64(totalNumLoc)
				}

				memUsage, length := c.cache.Stat()
				c.metrics.SetCacheLength(uint64(length))
				c.metrics.SetCacheMemory(uint64(memUsage))
				c.metrics.SetHits(hitsNumLoc)
				c.metrics.SetMisses(missesNumLoc)
				c.metrics.SetErrors(errorsNumLoc)
				c.metrics.SetRPS(totalNumLoc)
				c.metrics.SetAvgResponseTime(avgDuration)

				totalNum += totalNumLoc
				hitsNum += hitsNumLoc
				missesNum += missesNumLoc
				errorsNum += errorsNumLoc
				totalDurationNum += totalDurationNumLoc

				if i == logIntervalSecs {
					elapsed := time.Since(prev)
					duration := time.Duration(int(avgDuration))
					rps := float64(totalNumLoc) / elapsed.Seconds()

					hits.Store(0)
					misses.Store(0)
					errors.Store(0)
					totalDuration.Store(0)

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
							"[%s][%s] served %d requests (rps: %.f, avg.dur.: %s total: %d, errors: %d)",
							target, elapsed.String(), totalNum, rps, duration.String(), totalNum, errorsNum,
						)
					}

					totalNum = 0
					hitsNum = 0
					missesNum = 0
					errorsNum = 0
					totalDurationNum = 0
					prev = time.Now()
					i = 0
				}
				i++
			}
		}
	}()
}
