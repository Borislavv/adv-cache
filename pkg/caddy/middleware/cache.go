package advancedcache

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/header"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	httpwriter "github.com/Borislavv/advanced-cache/pkg/writer"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"net/http"
	"sync/atomic"
	"time"
	"unsafe"
)

var _ caddy.Module = (*CacheMiddleware)(nil)

// enabled indicates whether the advanced cache is turned on or off.
// It can be safely accessed and modified concurrently.
var enabled atomic.Bool

var (
	total         = &atomic.Uint64{}
	hits          = &atomic.Uint64{}
	misses        = &atomic.Uint64{}
	errors        = &atomic.Uint64{}
	totalDuration = &atomic.Int64{} // UnixNano
)

var (
	contentTypeKey                  = "Content-Type"
	applicationJsonValue            = "application/json"
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
	storage    storage.Storage
	backend    repository.Backender
	refresher  storage.Refresher
	evictor    storage.Evictor
	dumper     storage.Dumper
	metrics    metrics.Meter
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
	var from = time.Now()
	defer func() { totalDuration.Add(time.Since(from).Nanoseconds()) }()

	total.Add(1)
	if enabled.Load() {
		return m.handleThroughCache(w, r, next)
	} else {
		return m.handleThroughProxy(w, r, next)
	}
}

func (m *CacheMiddleware) handleThroughProxy(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	return next.ServeHTTP(w, r)
}

func (m *CacheMiddleware) handleThroughCache(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	newEntry, err := model.NewEntryNetHttp(m.cfg, r)
	if err != nil {
		if model.IsRouteWasNotFound(err) {
			return m.handleThroughProxy(w, r, next)
		}
		return err
	}

	var (
		responseStatus       int
		responseHeaders      *[][2][]byte
		responseBody         []byte
		responseLastModified int64
	)

	cacheEntry, found := m.storage.Get(newEntry)
	if !found {
		misses.Add(1)

		// MISS — prepare capture writer
		captured, releaseCapturer := httpwriter.NewCaptureResponseWriter(w)
		defer releaseCapturer()

		// run downstream handler
		if srvErr := next.ServeHTTP(captured, r); srvErr != nil {
			errors.Add(1)
			captured.Reset()
			captured.SetStatusCode(http.StatusServiceUnavailable)
			captured.WriteHeader(captured.StatusCode())
			_, _ = captured.Write(serviceTemporaryUnavailableBody)
		}

		// path is immutable and used only inside request
		path := unsafe.Slice(unsafe.StringData(r.URL.Path), len(r.URL.Path))

		// query immutable and used only inside request
		query := unsafe.Slice(unsafe.StringData(r.URL.RawQuery), len(r.URL.RawQuery))

		// Get query responseHeaders from original request
		queryHeaders, queryHeadersReleaser := newEntry.GetFilteredAndSortedKeyHeadersNetHttp(r)
		defer queryHeadersReleaser(queryHeaders)

		var extractReleaser func(*[][2][]byte)
		responseStatus, responseHeaders, responseBody, extractReleaser = captured.ExtractPayload()
		defer extractReleaser(responseHeaders)

		if responseStatus != http.StatusOK {
			errors.Add(1)

			// non-positive responseStatus code received, skip saving
			defer newEntry.Release(false)

			responseLastModified = time.Now().Unix()
		} else {
			// Save the response into the new newEntry
			newEntry.SetPayload(path, query, queryHeaders, responseHeaders, responseBody, responseStatus)
			newEntry.SetRevalidator(m.backend.RevalidatorMaker())

			// build and store new Entry in cache
			var wasPersisted bool
			cacheEntry, wasPersisted = m.storage.Set(model.NewVersionPointer(newEntry))
			if wasPersisted {
				defer cacheEntry.Release(false) // an Entry stored in the cache, must be released after use
			} else {
				defer cacheEntry.Release(true) // an Entry was not persisted, must be removed after use
			}

			responseLastModified = cacheEntry.UpdateAt()
		}
	} else {
		hits.Add(1)

		// deferred release and remove
		newEntry.Release(true)          // new Entry which was used as request for query cache does not need anymore
		defer cacheEntry.Release(false) // an Entry retrieved from the cache must be released after use

		// Always read from cached cacheEntry
		var queryHeaders *[][2][]byte
		var payloadReleaser func(q, h *[][2][]byte)
		_, _, queryHeaders, responseHeaders, responseBody, responseStatus, payloadReleaser, err = cacheEntry.Payload()
		defer payloadReleaser(queryHeaders, responseHeaders)
		if err != nil {
			errors.Add(1)

			// ERROR — prepare capture writer
			captured, releaseCapturer := httpwriter.NewCaptureResponseWriter(w)
			defer releaseCapturer()

			if srvErr := next.ServeHTTP(captured, r); srvErr != nil {
				errors.Add(1)
				captured.Reset()
				captured.SetStatusCode(http.StatusServiceUnavailable)
				captured.WriteHeader(captured.StatusCode())
				_, _ = captured.Write(serviceTemporaryUnavailableBody)
			}

			var extractReleaser func(*[][2][]byte)
			responseStatus, responseHeaders, responseBody, extractReleaser = captured.ExtractPayload()
			defer extractReleaser(responseHeaders)

			responseLastModified = time.Now().Unix()
		} else {
			responseLastModified = cacheEntry.UpdateAt()
		}
	}

	// Write cached responseHeaders
	for _, kv := range *responseHeaders {
		w.Header().Add(
			unsafe.String(unsafe.SliceData(kv[0]), len(kv[0])),
			unsafe.String(unsafe.SliceData(kv[1]), len(kv[1])),
		)
	}

	// Last-Modified
	lastModifiedBuffer, lastModifiedReleaser := header.SetLastModifiedValueNetHttp(w, responseLastModified)
	defer lastModifiedReleaser(lastModifiedBuffer)

	// Content-Type
	w.Header().Set(contentTypeKey, applicationJsonValue)

	// Write a response responseBody
	if _, err = w.Write(responseBody); err != nil {
		responseStatus = http.StatusServiceUnavailable
		return err
	}

	w.WriteHeader(responseStatus)

	return nil
}
