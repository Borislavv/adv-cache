package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Borislavv/advanced-cache/internal/cache/config"
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
	"strconv"
	"sync/atomic"
	"time"
)

// CacheGetPath for getting pagedata from cache via HTTP.
const CacheGetPath = "/{any:*}"

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
	totalDuration = &atomic.Int64{} // UnixNano
)

// CacheController handles cache API requests (read/write-through, error reporting, metrics).
type CacheController struct {
	cfg     *config.Config
	ctx     context.Context
	cache   storage.Storage
	metrics metrics.Meter
	backend repository.Backender
}

// NewCacheController builds a cache API controller with all dependencies.
// If debug is enabled, launches internal stats logger goroutine.
func NewCacheController(
	ctx context.Context,
	cfg *config.Config,
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
	c.runLoggerMetricsWriter(ctx)
	return c
}

// Index is the main HTTP handler.
func (c *CacheController) Index(r *fasthttp.RequestCtx) {
	var from = time.Now()

	// make a lightweight request Entry (contains only key, shardKey and fingerprint)
	newEntry, err := model.NewEntryFastHttp(c.cfg.Cache, r) // must be removed on hit and release on miss
	if err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	var (
		payloadStatus  int
		payloadHeaders *[][2][]byte
		payloadBody    []byte
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
			c.respondThatServiceIsTemporaryUnavailable(err, r)
			return
		}
		newEntry.SetPayload(path, queryString, queryHeaders, payloadHeaders, payloadBody, payloadStatus)
		newEntry.SetRevalidator(c.backend.RevalidatorMaker())

		// build and store new Entry in cache
		foundEntry = c.cache.Set(model.NewVersionPointer(newEntry))
		defer foundEntry.Release() // an Entry stored in the cache must be released after use
	} else {
		hits.Add(1)

		// deferred release and remove
		newEntry.Remove()          // new Entry which was used as request for query cache does not need anymore
		defer foundEntry.Release() // an Entry retrieved from the cache must be released after use

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
	header.SetLastModifiedFastHttp(r, foundEntry, payloadStatus)

	// Write payloadBody
	if _, err = serverutils.Write(payloadBody, r); err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	// Record the totalDuration in debug mode for metrics.
	totalDuration.Add(time.Since(from).Nanoseconds())
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

func (c *CacheController) runLoggerMetricsWriter(ctx context.Context) {
	go func() {
		metricsTicker := utils.NewTicker(ctx, time.Second)

		const loggerIntervalSecs = 5
		loggerTicker := utils.NewTicker(ctx, time.Second*loggerIntervalSecs)

		var (
			totalNum         uint64
			hitsNum          uint64
			missesNum        uint64
			totalDurationNum int64
		)

		for {
			select {
			case <-ctx.Done():
				return
			case <-metricsTicker:
				hitsNumLoc := hits.Load()
				missesNumLoc := misses.Load()
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
				c.metrics.SetRPS(totalNumLoc)
				c.metrics.SetAvgResponseTime(avgDuration)

				totalNum += totalNumLoc
				hitsNum += hitsNumLoc
				missesNum += missesNumLoc
				totalDurationNum += totalDurationNumLoc

				hits.Store(0)
				misses.Store(0)
				totalDuration.Store(0)
			case <-loggerTicker:
				rps := totalNum / loggerIntervalSecs
				avgDuration := uint64(totalDurationNum) / totalNum

				logEvent := log.Info()

				if c.cfg.IsProd() {
					logEvent.
						Str("target", "controller").
						Str("rps", strconv.Itoa(int(rps))).
						Str("served", strconv.Itoa(int(totalNum))).
						Str("periodMs", strconv.Itoa(loggerIntervalSecs*1000)).
						Str("avgDuration", strconv.Itoa(int(avgDuration)))
				}

				logEvent.Msgf(
					"[controller][5s] served %d requests (rps: %d, avg.dur.: %d hits: %d, misses: %d)",
					totalNum, rps, avgDuration, hitsNum, missesNum,
				)

				totalNum = 0
				hitsNum = 0
				missesNum = 0
				totalDurationNum = 0
			}
		}
	}()
}
