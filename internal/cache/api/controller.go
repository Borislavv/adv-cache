package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Borislavv/advanced-cache/internal/cache/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	serverutils "github.com/Borislavv/advanced-cache/pkg/server/utils"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
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

	hdrLastModified = []byte("Last-Modified")
)

// Buffered channel for request durations (used only if debug enabled)
var (
	count    = &atomic.Int64{} // Num
	duration = &atomic.Int64{} // UnixNano
)

// CacheController handles cache API requests (read/write-through, error reporting, metrics).
type CacheController struct {
	cfg         *config.Config
	ctx         context.Context
	cache       storage.Storage
	backend     repository.Backender
	sharededMap *sharded.Map[*model.Entry]
}

// NewCacheController builds a cache API controller with all dependencies.
// If debug is enabled, launches internal stats logger goroutine.
func NewCacheController(
	ctx context.Context,
	cfg *config.Config,
	cache storage.Storage,
	backend repository.Backender,
) *CacheController {
	shardedMap := sharded.NewMap[*model.Entry](ctx, 32)

	c := &CacheController{
		cfg:         cfg,
		ctx:         ctx,
		cache:       cache,
		backend:     backend,
		sharededMap: shardedMap,
	}
	go func() {

		log.Info().Msg("[cache-controller] data loading")
		defer log.Info().Msg("[cache-controller] data loading finished")

		i := 0
		path := []byte("/api/v2/pagedata")
		for resp := range mock.StreamRandomResponses(ctx, c.cfg.Cache, path, 10_000_000) {
			entry, err := model.NewEntryManual(cfg.Cache, path, resp.ToQuery(), resp.Request().Headers())
			if err != nil {
				log.Error().Err(err).Msg("error creating entry")
				return
			}
			entry.SetPayload(path, resp.ToQuery(), resp.Data().Body(), resp.Data().Headers(), resp.Data().StatusCode())

			c.sharededMap.Set(entry)

			if i%1_000_000 == 0 {
				log.Info().Msgf("[cache-controller] map len: %d", c.sharededMap.Len())
			}
			i++
		}
		log.Info().Msgf("[cache-controller] last: map len: %d", c.sharededMap.Len())

		var cnt = &atomic.Int64{}
		c.sharededMap.WalkShards(func(key uint64, shard *sharded.Shard[*model.Entry]) {
			shard.Walk(ctx, func(u uint64, entry *model.Entry) bool {
				fmt.Printf("key: %d, shard: %d, entry: %+v \n", key, u, entry)
				if cnt.Add(1) > 100 {
					return false
				}
				return true
			}, false)
		})
	}()
	c.runLogger(ctx)
	return c
}

// Index is the main HTTP handler for /api/v1/cache.
func (c *CacheController) Index(r *fasthttp.RequestCtx) {
	var from = time.Now()

	entry, err := model.NewEntry(c.cfg.Cache, r)
	if err != nil {
		log.Error().Err(err).Msg("error creating entry")
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	v, found := c.sharededMap.Get(entry.MapKey(), entry.ShardKey())
	if !found {
		panic("not found")
	}
	_, _, status, headers, body := v.Payload()

	// Write status, headers, and body from the cached (or fetched) response.
	r.Response.SetStatusCode(status)
	for key, vv := range headers {
		for _, value := range vv {
			r.Response.Header.Add(key, value)
		}
	}

	// Set up Last-Modified header
	if _, err := serverutils.Write(body, r); err != nil {
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
