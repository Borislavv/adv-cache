package middleware

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
	"net/http"
	"sync/atomic"
	"time"
	"unsafe"
)

var (
	contentTypeKey       = "Content-Type"
	applicationJsonValue = "application/json"
)

// enabled indicates whether the advanced cache is turned on or off.
// It can be safely accessed and modified concurrently.
var enabled atomic.Bool

var (
	hits          = &atomic.Uint64{}
	misses        = &atomic.Uint64{}
	errors        = &atomic.Uint64{}
	totalDuration = &atomic.Int64{} // UnixNano
)

type TraefikCacheMiddleware struct {
	ctx       context.Context
	next      http.Handler
	name      string
	cfg       *config.Cache
	storage   storage.Storage
	backend   repository.Backender
	refresher lru.Refresher
	evictor   lru.Evictor
	dumper    storage.Dumper
	metrics   metrics.Meter
	count     int64 // Num
	duration  int64 // UnixNano
}

func New(ctx context.Context, next http.Handler, name string) http.Handler {
	cacheMiddleware := &TraefikCacheMiddleware{
		ctx:  ctx,
		next: next,
		name: name,
	}

	if err := cacheMiddleware.run(ctx); err != nil {
		panic(err)
	}

	return cacheMiddleware
}

func (m *TraefikCacheMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var from = time.Now()
	defer func() { totalDuration.Add(time.Since(from).Nanoseconds()) }()

	if enabled.Load() {
		m.handleThroughCache(w, r)
	} else {
		hits.Add(1)
		m.next.ServeHTTP(w, r)
	}
	return
}

func (m *TraefikCacheMiddleware) handleThroughCache(w http.ResponseWriter, r *http.Request) {
	newEntry, err := model.NewEntryNetHttp(m.cfg, r)
	if err != nil {
		errors.Add(1)
		// Path was not matched, then handle request through upstream without cache.
		m.next.ServeHTTP(w, r)
		return
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
		m.next.ServeHTTP(captured, r)

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

			m.next.ServeHTTP(captured, r)

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
}
