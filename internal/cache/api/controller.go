package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Borislavv/advanced-cache/internal/cache/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/pools"
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

// Predefined HTTP response templates for error handling (400/503)
var (
	serviceUnavailableResponseBytes = []byte(`{
	  "status": 503,
	  "error": "Service Unavailable",
	  "message": "` + string(messagePlaceholder) + `"
	}`)
	messagePlaceholder = []byte("${message}")
	hdrLastModified    = []byte("Last-Modified")
)

// Buffered channel for request durations (used only if debug enabled)
var (
	count    = &atomic.Int64{} // Num
	duration = &atomic.Int64{} // UnixNano
	fnd      = &atomic.Int64{}
	notFnd   = &atomic.Int64{}
)

// CacheController handles cache API requests (read/write-through, error reporting, metrics).
type CacheController struct {
	cfg     *config.Config
	ctx     context.Context
	cache   storage.Storage
	backend repository.Backender
}

// NewCacheController builds a cache API controller with all dependencies.
// If debug is enabled, launches internal stats logger goroutine.
func NewCacheController(
	ctx context.Context,
	cfg *config.Config,
	cache storage.Storage,
	backend repository.Backender,
) *CacheController {
	c := &CacheController{
		cfg:     cfg,
		ctx:     ctx,
		cache:   cache,
		backend: backend,
	}
	c.runLogger(ctx)
	return c
}

func (c *CacheController) queryHeaders(r *fasthttp.RequestCtx) (headers [][2][]byte, releaseFn func()) {
	headers = pools.KeyValueSlicePool.Get().([][2][]byte)
	r.Request.Header.All()(func(key []byte, value []byte) bool {
		headers = append(headers, [2][]byte{key, value})
		return true
	})
	return headers, func() {
		headers = headers[:0]
		pools.KeyValueSlicePool.Put(headers)
	}
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
		payloadHeaders [][2][]byte
		payloadBody    []byte
	)

	foundEntry, found := c.cache.Get(newEntry)
	if !found {
		notFnd.Add(1)

		// extract request data
		path := r.Path()
		rule := newEntry.Rule()
		queryString := r.QueryArgs().QueryString()
		queryHeaders, queryReleaser := c.queryHeaders(r)
		defer queryReleaser()

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
		fnd.Add(1)

		// deferred release and remove
		newEntry.Remove()          // new Entry which was used as request for query cache does not need anymore
		defer foundEntry.Release() // an Entry retrieved from the cache must be released after use

		// unpack found Entry data
		var queryHeaders [][2][]byte
		var payloadReleaser func(q, h [][2][]byte)
		_, _, queryHeaders, payloadHeaders, payloadBody, payloadStatus, payloadReleaser, err = foundEntry.Payload()
		defer payloadReleaser(queryHeaders, payloadHeaders)
		if err != nil {
			c.respondThatServiceIsTemporaryUnavailable(err, r)
			return
		}
	}

	// Write payloadStatus, payloadHeaders, and payloadBody from the cached (or fetched) response.
	r.Response.SetStatusCode(payloadStatus)
	for _, kv := range payloadHeaders {
		r.Response.Header.AddBytesKV(kv[0], kv[1])
	}

	// Set up Last-Modified header
	c.setLastModifiedHeader(r, foundEntry, payloadStatus)

	// Write payloadBody
	if _, err = serverutils.Write(payloadBody, r); err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	// Record the duration in debug mode for metrics.
	count.Add(1)
	duration.Add(time.Since(from).Nanoseconds())
}

// respondThatServiceIsTemporaryUnavailable returns 503 and logs the error.
func (c *CacheController) respondThatServiceIsTemporaryUnavailable(err error, ctx *fasthttp.RequestCtx) {
	log.Error().Err(err).Msg("[cache-controller] handle request error: " + err.Error()) // Don't move it down due to error will be rewritten.

	ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
	if _, err = serverutils.Write(c.resolveMessagePlaceholder(serviceUnavailableResponseBytes, err), ctx); err != nil {
		log.Err(err).Msg("failed to write into *fasthttp.RequestCtx")
	}
}

func (c *CacheController) setLastModifiedHeader(r *fasthttp.RequestCtx, entry *model.VersionPointer, status int) {
	var t time.Time
	if status == http.StatusOK {
		t = time.Unix(0, entry.WillUpdateAt()-entry.Rule().TTL.Nanoseconds())
	} else {
		t = time.Unix(0, entry.WillUpdateAt()-entry.Rule().ErrorTTL.Nanoseconds())
	}

	buf := fasthttp.AppendHTTPDate(nil, t) // zero-alloc форматирование в RFC1123 (http.TimeFormat)
	r.Response.Header.SetBytesKV(hdrLastModified, buf)
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

// runLogger runs a goroutine to periodically log RPS and avg duration per window, if debug enabled.
func (c *CacheController) runLogger(ctx context.Context) {
	go func() {
		t := utils.NewTicker(ctx, time.Second*5)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t:
				c.logAndReset()
			}
		}
	}()
}

// logAndReset prints and resets stat counters for a given window (5s).
func (c *CacheController) logAndReset() {
	const secs int64 = 5

	var (
		avg string
		cnt = count.Load()
		dur = time.Duration(duration.Load())
		rps = strconv.Itoa(int(cnt / secs))
	)

	if cnt <= 0 {
		return
	}

	avg = (dur / time.Duration(cnt)).String()

	logEvent := log.Info()

	if c.cfg.IsProd() {
		logEvent.
			Str("target", "controller").
			Str("rps", rps).
			Str("served", strconv.Itoa(int(cnt))).
			Str("periodMs", "5000").
			Str("avgDuration", avg)
	}

	logEvent.Msgf(
		"[controller][5s] served %d requests (rps: %s, avgDuration: %s), hits: %d, misses: %d",
		cnt, rps, avg, fnd.Load(), notFnd.Load(),
	)

	count.Store(0)
	duration.Store(0)
}
