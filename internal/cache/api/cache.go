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
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
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
	errors        = &atomic.Int64{}
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
	var from = time.Now()
	defer func() { totalDuration.Add(time.Since(from).Nanoseconds()) }()

	total.Add(1)
	if enabled.Load() {
		c.handleTroughCache(r)
	} else {
		c.handleTroughProxy(r)
	}
}

func (c *CacheController) handleTroughProxy(r *fasthttp.RequestCtx) {
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

	foundEntry, found := c.cache.Get(newEntry) // newEntry - terminates right here on found case
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
			newEntry.Remove()
			payloadLastModified = time.Now().UnixNano()
		} else {
			newEntry.SetPayload(path, queryString, queryHeaders, payloadHeaders, payloadBody, payloadStatus)
			newEntry.SetRevalidator(c.backend.RevalidatorMaker())

			// build and store new VersionPointer in cache
			newEntryPointer := c.cache.Set(model.NewVersionPointer(newEntry))
			payloadLastModified = newEntryPointer.UpdateAt()
			newEntryPointer.Release()
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
			foundEntry.Release()
			c.respondThatServiceIsTemporaryUnavailable(err, r)
			return
		}

		foundEntry.Release()
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
			totalNum         int64
			hitsNum          int64
			missesNum        int64
			errorsNum        int64
			proxiedNum       int64
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
				totalNumLoc := total.Swap(0)
				hitsNumLoc := hits.Swap(0)
				missesNumLoc := misses.Swap(0)
				errorsNumLoc := errors.Swap(0)
				proxiedNumLoc := totalNumLoc - hitsNumLoc - missesNumLoc - errorsNumLoc
				totalDurationNumLoc := totalDuration.Swap(0)

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

				acquired := model.Acquired.Load()
				released := model.Released.Load()
				removed := model.Removed.Load()
				finalized := model.Finalized.Load()
				doomedMarked := model.DoomedMarked.Load()

				setTheSamePointer := lru.SetTheSamePointer.Load()
				setFoundAndUpdated := lru.SetFoundAndUpdated.Load()
				setFoundAndTouched := lru.SetFoundAndTouched.Load()
				setInserted := lru.SetInsertedNewOne.Load()
				setNotAdmitted := lru.SetNotAdmitted.Load()

				getFound := lru.GetFound.Load()
				getNotFound := lru.GetNotFound.Load()
				getNotAcquired := lru.GetNotAcquired.Load()
				getHashCollision := lru.GetNotTheSameFingerprint.Load()
				getRequestsAcquired := lru.GetRequestsAcquired.Load()

				notReleasedYet := acquired - (released + removed)
				notFinalizedYet := (removed + doomedMarked) - finalized
				set := setTheSamePointer + setFoundAndUpdated + setInserted + setNotAdmitted
				get := getFound + getNotFound + getNotAcquired + getHashCollision

				if i == logIntervalSecs {
					log.Info().Msgf(
						"[refCounting] notReleasedYet: %d (acquired: %d - (released: %d + removed: %d)), "+
							"notFinalizedYet: %d (finalized: %d - (removed: %d + markedAsDoomed: %d)), "+
							"get: %d (found: %d, notFound: %d, notAcquired: %d, hashCollision: %d, reqAcquired: %d), "+
							"set: %d (setTheSamePointer: %d, setFoundAndTouched: %d, setFoundAndUpdated: %d, setInserted: %d, setNotAdmitted: %d)",
						notReleasedYet, acquired, released, removed,
						notFinalizedYet, finalized, removed, doomedMarked,
						get, getFound, getNotFound, getNotAcquired, getHashCollision, getRequestsAcquired,
						set, setTheSamePointer, setFoundAndTouched, setFoundAndUpdated, setInserted, setNotAdmitted,
					)

					elapsed := time.Since(prev)
					duration := time.Duration(int(avgDuration))
					rps := float64(totalNum) / elapsed.Seconds()

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
							"[%s][%s] served %d requests (rps: %.f, avg.dur.: %s hits: %d, misses: %d, proxied: %d, errors: %d)",
							target, elapsed.String(), totalNum, rps, duration.String(), hitsNum, missesNum, proxiedNum, errorsNum,
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
			}
		}
	}()
}
