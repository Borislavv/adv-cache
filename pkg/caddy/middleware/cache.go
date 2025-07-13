package advancedcache

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/rs/zerolog/log"
	"net/http"
	"sync/atomic"
	"time"
	"unsafe"
)

var _ caddy.Module = (*CacheMiddleware)(nil)

var (
	contentTypeKey                  = "Content-Type"
	applicationJsonValue            = "application/json"
	lastModifiedKey                 = "Last-Modified"
	serviceTemporaryUnavailableBody = []byte(`{"error":{"message":"Service temporarily unavailable."}}`)
)

const moduleName = "advanced_cache"

func init() {
	caddy.RegisterModule(&CacheMiddleware{})
	httpcaddyfile.RegisterHandlerDirective(moduleName, parseCaddyfile)
}

type CacheMiddleware struct {
	ConfigPath string
	ctx        context.Context
	cfg        *config.Cache
	store      storage.Storage
	backend    repository.Backender
	refresher  storage.Refresher
	evictor    storage.Evictor
	dumper     storage.Dumper
	count      int64 // Num
	duration   int64 // UnixNano
}

func (*CacheMiddleware) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers." + moduleName,
		New: func() caddy.Module { return new(CacheMiddleware) },
	}
}

func (m *CacheMiddleware) Provision(ctx caddy.Context) error {
	return m.run(ctx.Context)
}

func (m *CacheMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	from := time.Now()

	// Build request (return error on rule missing for current path)
	newEntry, releaser, err := model.NewEntryNetHttp(m.cfg, r)
	defer releaser()
	if err != nil {
		// If path does not match then process request manually without cache
		return next.ServeHTTP(w, r)
	}

	entry, isHit := m.store.Get(newEntry)
	if !isHit {
		captured, capturedReleaser := newCaptureRW()
		defer capturedReleaser()

		// Handle request manually due to store it
		if srvErr := next.ServeHTTP(captured, r); srvErr != nil {
			captured.reset()
			captured.status = http.StatusServiceUnavailable
			captured.WriteHeader(captured.status)
			_, _ = captured.Write(serviceTemporaryUnavailableBody)
		}

		// Set up new entry
		m.store.Set(newEntry)
		entry = newEntry
	}

	_, _, _, headers, body, status, payloadReleaser, err := entry.Payload()
	defer payloadReleaser()
	if err != nil {
		log.Error().Err(err).Msg("error occurred while unpacking payload")
		return next.ServeHTTP(w, r)
	}

	// Apply custom http headers
	for _, kv := range headers {
		w.Header().Add(
			unsafe.String(unsafe.SliceData(kv[0]), len(kv[0])),
			unsafe.String(unsafe.SliceData(kv[1]), len(kv[1])),
		)
	}

	// Apply standard http headers
	w.WriteHeader(status)
	m.setLastModifiedHeader(w, entry, status)
	w.Header().Add(contentTypeKey, applicationJsonValue)

	// Write response data
	_, _ = w.Write(body)

	// Record the duration in debug mode for metrics.
	atomic.AddInt64(&m.count, 1)
	atomic.AddInt64(&m.duration, time.Since(from).Nanoseconds())

	return err
}

func (m *CacheMiddleware) setLastModifiedHeader(w http.ResponseWriter, entry *model.Entry, status int) {
	if status == http.StatusOK {
		bts := time.Unix(0, entry.WillUpdateAt()-entry.Rule().TTL.Nanoseconds()).AppendFormat(nil, http.TimeFormat)
		w.Header().Set(lastModifiedKey, unsafe.String(unsafe.SliceData(bts), len(bts)))
	} else {
		bts := time.Unix(0, entry.WillUpdateAt()-entry.Rule().ErrorTTL.Nanoseconds()).AppendFormat(nil, http.TimeFormat)
		w.Header().Set(lastModifiedKey, unsafe.String(unsafe.SliceData(bts), len(bts)))
	}
}
