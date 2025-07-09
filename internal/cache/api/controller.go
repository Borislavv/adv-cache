package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Borislavv/advanced-cache/internal/cache/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/Borislavv/advanced-cache/pkg/model"
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
	go func() {
		log.Info().Msg("[cache-controller] data loading")
		defer log.Info().Msg("[cache-controller] data loading finished")

		path := []byte("/api/v2/pagedata")
		for resp := range mock.StreamRandomResponses(ctx, c.cfg.Cache, path, 10_000_000) {
			entry, err := model.NewEntryManual(cfg.Cache, path, resp.ToQuery(), resp.Request().Headers(), c.backend.RevalidatorMaker())
			if err != nil {
				log.Error().Err(err).Msg("error creating entry")
				return
			}
			entry.SetPayload(path, resp.ToQuery(), resp.Request().Headers(), resp.Data().Body(), resp.Data().Headers(), resp.Data().StatusCode())
			c.cache.Set(entry)
		}
	}()
	c.runLogger(ctx)
	return c
}

func (c *CacheController) queryHeaders(r *fasthttp.RequestCtx) [][2][]byte {
	queryHeaders := make([][2][]byte, 0, r.Request.Header.Len())
	r.Request.Header.All()(func(key []byte, value []byte) bool {
		k := make([]byte, len(key))
		val := make([]byte, len(value))
		queryHeaders = append(queryHeaders, [2][]byte{k, val})
		return true
	})
	return queryHeaders
}

// Index is the main HTTP handler.
func (c *CacheController) Index(r *fasthttp.RequestCtx) {
	var from = time.Now()

	entry, err := model.NewEntry(c.cfg.Cache, r)
	if err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	var (
		status  int
		headers http.Header
		body    []byte
	)

	v, found := c.cache.Get(entry.MapKey(), entry.ShardKey())
	if !found {
		path := r.Path()
		queryHeaders := c.queryHeaders(r)
		queryString := r.QueryArgs().QueryString()
		status, headers, body, err = c.backend.Fetch(c.ctx, path, queryString, queryHeaders)
		if err != nil {
			c.respondThatServiceIsTemporaryUnavailable(err, r)
			return
		}
		entry.SetPayload(path, queryString, queryHeaders, body, headers, status)
		entry.SetRevalidator(c.backend.RevalidatorMaker())
		c.cache.Set(entry)
		v = entry
	} else {
		_, _, _, status, headers, body, err = v.Payload()
		if err != nil {
			c.respondThatServiceIsTemporaryUnavailable(err, r)
			return
		}
	}

	// Write status, headers, and body from the cached (or fetched) response.
	r.Response.SetStatusCode(status)
	for key, vv := range headers {
		for _, value := range vv {
			r.Response.Header.Add(key, value)
		}
	}

	// Set up Last-Modified header
	c.setLastModifiedHeader(r, v, status)

	// Write body
	if _, err = serverutils.Write(body, r); err != nil {
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

func (c *CacheController) setLastModifiedHeader(r *fasthttp.RequestCtx, entry *model.Entry, status int) {
	if status == http.StatusOK {
		r.Request.Header.SetBytesKV(
			hdrLastModified,
			time.Unix(0, entry.WillUpdateAt()-entry.Rule().TTL.Nanoseconds()).AppendFormat(nil, http.TimeFormat),
		)
	} else {
		r.Request.Header.SetBytesKV(
			hdrLastModified,
			time.Unix(0, entry.WillUpdateAt()-entry.Rule().ErrorTTL.Nanoseconds()).AppendFormat(nil, http.TimeFormat),
		)
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

	logEvent.Msgf("[controller][5s] served %d requests (rps: %s, avgDuration: %s)", cnt, rps, avg)

	count.Store(0)
	duration.Store(0)
}
