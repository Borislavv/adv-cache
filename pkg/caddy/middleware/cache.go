package advancedcache

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/header"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
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
	refresher  lru.Refresher
	evictor    lru.Evictor
	dumper     storage.Dumper
	metrics    metrics.Meter
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
	var from = time.Now()
	defer func() { totalDuration.Add(time.Since(from).Nanoseconds()) }()

	if enabled.Load() {
		if err := m.handleThroughCache(w, r, next); err != nil {
			return err
		}
	} else {
		hits.Add(1)
		if err := next.ServeHTTP(w, r); err != nil {
			errors.Add(1)
			return err
		}
	}
	return nil
}

func (m *CacheMiddleware) handleThroughCache(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	newEntry, err := model.NewEntryNetHttp(m.cfg, r)
	if err != nil {
		errors.Add(1)
		// Path was not matched, then handle request through upstream without cache.
		return next.ServeHTTP(w, r)
	}

	var (
		status  int
		headers *[][2][]byte
		body    []byte
	)

	foundEntry, found := m.storage.Get(newEntry)
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

		// Get query headers from original request
		queryHeaders, queryHeadersReleaser := newEntry.GetFilteredAndSortedKeyHeadersNetHttp(r)
		defer queryHeadersReleaser(queryHeaders)

		var extractReleaser func(*[][2][]byte)
		status, headers, body, extractReleaser = captured.ExtractPayload()
		defer extractReleaser(headers)

		if status != http.StatusOK {
			errors.Add(1)
		} else {
			// Save the response into the new newEntry
			newEntry.SetPayload(path, query, queryHeaders, headers, body, status)
			newEntry.SetRevalidator(m.backend.RevalidatorMaker())

			// build and store new Entry in cache
			m.storage.Set(newEntry)
		}
	} else {
		hits.Add(1)

		// Always read from cached foundEntry
		var queryHeaders *[][2][]byte
		var payloadReleaser func(q, h *[][2][]byte)
		_, _, queryHeaders, headers, body, status, payloadReleaser, err = foundEntry.Payload()
		defer payloadReleaser(queryHeaders, headers)
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
			status, headers, body, extractReleaser = captured.ExtractPayload()
			defer extractReleaser(headers)
		}
	}

	// Write cached headers
	for _, kv := range *headers {
		w.Header().Add(
			unsafe.String(unsafe.SliceData(kv[0]), len(kv[0])),
			unsafe.String(unsafe.SliceData(kv[1]), len(kv[1])),
		)
	}

	// Last-Modified
	header.SetLastModifiedNetHttp(w, foundEntry)

	// Content-Type
	w.Header().Set(contentTypeKey, applicationJsonValue)

	// StatusCode-code
	w.WriteHeader(status)

	// Write a response body
	_, _ = w.Write(body)

	// Metrics
	atomic.AddInt64(&m.count, 1)

	return nil
}
