package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Borislavv/traefik-http-cache-plugin/internal/cache/config"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/repository"
	serverutils "github.com/Borislavv/traefik-http-cache-plugin/pkg/server/utils"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/utils"
	"github.com/fasthttp/router"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

// CacheGetPath for getting pagedata from cache via HTTP.
const CacheGetPath = "/api/v1/cache"

// Predefined HTTP response templates for error handling (400/503)
var (
	badRequestResponseBytes = []byte(`{
	  "status": 400,
	  "error": "Bad Request",
	  "message": "` + string(messagePlaceholder) + `"
	}`)
	serviceUnavailableResponseBytes = []byte(`{
	  "status": 503,
	  "error": "Service Unavailable",
	  "message": "` + string(messagePlaceholder) + `"
	}`)
	messagePlaceholder = []byte("${message}")
	zeroLiteral        = "0"
)

// Buffered channel for request durations (used only if debug enabled)
var (
	durCh chan time.Duration
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
	if c.cfg.IsDebugOn() {
		c.runLogger(ctx)
	}
	return c
}

// Index is the main HTTP handler for /api/v1/cache.
func (c *CacheController) Index(r *fasthttp.RequestCtx) {
	f := time.Now()

	// Extract application context from request, fallback to base context.
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()

	// Parse request parameters.
	req, err := model.NewRequest(r.QueryArgs())
	if err != nil {
		c.respondThatTheRequestIsBad(err, r)
		return
	}

	// Try to get response from cache.
	resp, found := c.cache.Get(req)
	if !found {
		// On cache miss, get data from upstream backend and save in cache.
		resp, err = c.backend.Fetch(ctx, req)
		if err != nil {
			c.respondThatServiceIsTemporaryUnavailable(err, r)
			return
		}
		c.cache.Set(resp)
	}

	// Write status, headers, and body from the cached (or fetched) response.
	data := resp.Data()
	r.Response.SetStatusCode(data.StatusCode())
	for key, vv := range data.Headers() {
		for _, value := range vv {
			r.Response.Header.Add(key, value)
		}
	}
	// Add revalidation time as Last-Modified
	r.Response.Header.Add("Last-Modified", resp.RevalidatedAt().Format(http.TimeFormat))

	if _, err = serverutils.Write(data.Body(), r); err != nil {
		c.respondThatServiceIsTemporaryUnavailable(err, r)
		return
	}

	// Record the duration in debug mode for metrics.
	if c.cfg.IsDebugOn() {
		select {
		case <-c.ctx.Done():
			return
		case durCh <- time.Since(f):
		default:
		}
	}
}

// respondThatServiceIsTemporaryUnavailable returns 503 and logs the error.
func (c *CacheController) respondThatServiceIsTemporaryUnavailable(err error, ctx *fasthttp.RequestCtx) {
	log.Error().Err(err).Msg("[cache-controller] handle request error: " + err.Error()) // Don't move it down due to error will be rewritten.

	ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
	if _, err = serverutils.Write(c.resolveMessagePlaceholder(serviceUnavailableResponseBytes, err), ctx); err != nil {
		log.Err(err).Msg("failed to write into *fasthttp.RequestCtx")
	}
}

// respondThatTheRequestIsBad returns 400 and logs the error.
func (c *CacheController) respondThatTheRequestIsBad(err error, ctx *fasthttp.RequestCtx) {
	log.Err(err).Msg("[cache-controller] bad request: " + err.Error()) // Don't move it down due to error will be rewritten.

	ctx.SetStatusCode(fasthttp.StatusBadRequest)
	if _, err = serverutils.Write(c.resolveMessagePlaceholder(badRequestResponseBytes, err), ctx); err != nil {
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

// stat is an internal structure for windowed request statistics (for debug logging).
type stat struct {
	label    string
	divider  int // window size in seconds
	tickerCh <-chan time.Time
	count    int
	total    time.Duration
}

// runLogger runs a goroutine to periodically log RPS and avg duration per window, if debug enabled.
func (c *CacheController) runLogger(ctx context.Context) {
	durCh = make(chan time.Duration, runtime.GOMAXPROCS(0))

	go func() {
		stats := []*stat{
			{label: "5s", divider: 5, tickerCh: utils.NewTicker(ctx, 5*time.Second)},
			{label: "1m", divider: 60, tickerCh: utils.NewTicker(ctx, time.Minute)},
			{label: "5m", divider: 300, tickerCh: utils.NewTicker(ctx, 5*time.Minute)},
			{label: "1h", divider: 3600, tickerCh: utils.NewTicker(ctx, time.Hour)},
		}

		for {
			select {
			case <-ctx.Done():
				return
			case dur := <-durCh:
				for _, s := range stats {
					s.count++
					s.total += dur
				}
			case <-stats[0].tickerCh:
				c.logAndReset(stats[0])
			case <-stats[1].tickerCh:
				c.logAndReset(stats[1])
			case <-stats[2].tickerCh:
				c.logAndReset(stats[2])
			case <-stats[3].tickerCh:
				c.logAndReset(stats[3])
			}
		}
	}()
}

// logAndReset prints and resets stat counters for a given window (5s, 1m, etc).
func (c *CacheController) logAndReset(s *stat) {
	var avg string
	if s.count > 0 {
		avg = (s.total / time.Duration(s.count)).String()
	} else {
		avg = zeroLiteral
	}
	rps := strconv.Itoa(s.count / s.divider)
	log.
		Info().
		//Str("target", "cache-controller").
		//Str("period", s.label).
		//Str("rps", rps).
		//Str("avgDuration", avg).
		Msgf("[cache-controller][%s] served %d requests (rps: %s, avgDuration: %s)", s.label, s.count, rps, avg)
	s.count = 0
	s.total = 0
}
