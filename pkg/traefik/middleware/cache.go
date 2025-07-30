package middleware

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
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	httpwriter "github.com/Borislavv/advanced-cache/pkg/writer"
	"github.com/rs/zerolog/log"
	"net/http"
	"sync/atomic"
	"time"
	"unsafe"
)

var (
	contentTypeKey       = "Content-Type"
	applicationJsonValue = "application/json"
)

// Predefined HTTP response templates for error handling (400/503)
var (
	serviceUnavailableResponseBytes = []byte(`{
	  "status": 503,
	  "error": "Service Unavailable",
	  "message": "` + string(messagePlaceholder) + `"
	}`)
	messagePlaceholder = []byte("${message}")
)

// enabled indicates whether the advanced cache is turned on or off.
// It can be safely accessed and modified concurrently.
var enabled atomic.Bool

var (
	total         = &atomic.Int64{}
	hits          = &atomic.Int64{}
	misses        = &atomic.Int64{}
	proxies       = &atomic.Int64{}
	errors        = &atomic.Int64{}
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

	total.Add(1)
	if enabled.Load() {
		m.handleThroughCache(w, r)
	} else {
		m.handleThroughProxy(w, r)
	}
	return
}

var (
	// if you return a releaser as an outer variable it will not allocate closure each time on call function
	queryHeadersReleaser = func(headers *[][2][]byte) {
		*headers = (*headers)[:0]
		pools.KeyValueSlicePool.Put(headers)
	}
)

func (m *TraefikCacheMiddleware) queryHeaders(r *http.Request) (headers *[][2][]byte, releaseFn func(*[][2][]byte)) {
	headers = pools.KeyValueSlicePool.Get().(*[][2][]byte)
	for key, vv := range r.Header {
		for _, value := range vv {
			*headers = append(*headers, [2][]byte{
				unsafe.Slice(unsafe.StringData(key), len(key)),
				unsafe.Slice(unsafe.StringData(value), len(value)),
			})
		}
	}
	return headers, queryHeadersReleaser
}

func (m *TraefikCacheMiddleware) handleThroughProxy(w http.ResponseWriter, r *http.Request) {
	proxies.Add(1)

	// extract request data
	path := unsafe.Slice(unsafe.StringData(r.URL.Path), len(r.URL.Path))
	query := unsafe.Slice(unsafe.StringData(r.URL.RawQuery), len(r.URL.RawQuery))
	queryHeaders, queryReleaser := m.queryHeaders(r)
	defer queryReleaser(queryHeaders)

	// fetch data from upstream
	status, headers, body, payloadReleaser, err := m.backend.Fetch(nil, path, query, queryHeaders)
	defer payloadReleaser()
	if err != nil {
		m.respondThatServiceIsTemporaryUnavailable(w, err)
		return
	}

	// Write cached headers
	for _, kv := range *headers {
		w.Header().Add(
			unsafe.String(unsafe.SliceData(kv[0]), len(kv[0])),
			unsafe.String(unsafe.SliceData(kv[1]), len(kv[1])),
		)
	}

	// Last-Modified
	header.SetLastModifiedValueNetHttp(w, time.Now().UnixNano())

	// Content-Type
	w.Header().Set(contentTypeKey, applicationJsonValue)

	// StatusCode-code
	w.WriteHeader(status)

	// Write a response body
	_, _ = w.Write(body)
}

func (m *TraefikCacheMiddleware) handleThroughCache(w http.ResponseWriter, r *http.Request) {
	newEntry, err := model.NewEntryNetHttp(m.cfg, r)
	if err != nil {
		if model.IsRouteWasNotFound(err) {
			m.handleThroughProxy(w, r)
			return
		}
		m.respondThatServiceIsTemporaryUnavailable(w, err)
		return
	}

	var (
		status       int
		headers      *[][2][]byte
		body         []byte
		lastModified int64
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
		queryHeaders, filteredQueryHeadersReleaser := newEntry.GetFilteredAndSortedKeyHeadersNetHttp(r)
		defer filteredQueryHeadersReleaser(queryHeaders)

		var extractReleaser func(*[][2][]byte)
		status, headers, body, extractReleaser = captured.ExtractPayload()
		defer extractReleaser(headers)

		if status != http.StatusOK {
			errors.Add(1)
			lastModified = time.Now().UnixNano()
		} else {
			// Save the response into the new newEntry
			newEntry.SetPayload(path, query, queryHeaders, headers, body, status)
			newEntry.SetRevalidator(m.backend.RevalidatorMaker())

			// build and store new Entry in cache
			m.storage.Set(newEntry)

			lastModified = newEntry.UpdateAt()
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

		lastModified = newEntry.UpdateAt()
	}

	// Write cached headers
	for _, kv := range *headers {
		w.Header().Add(
			unsafe.String(unsafe.SliceData(kv[0]), len(kv[0])),
			unsafe.String(unsafe.SliceData(kv[1]), len(kv[1])),
		)
	}

	// Last-Modified
	header.SetLastModifiedValueNetHttp(w, lastModified)

	// Content-Type
	w.Header().Set(contentTypeKey, applicationJsonValue)

	// StatusCode-code
	w.WriteHeader(status)

	// Write a response body
	_, _ = w.Write(body)
}

// respondThatServiceIsTemporaryUnavailable returns 503 and logs the error using net/http.
func (m *TraefikCacheMiddleware) respondThatServiceIsTemporaryUnavailable(w http.ResponseWriter, err error) {
	log.Error().Err(err).Msg("[cache-controller] handle request error") // Don't move it down due to error will be rewritten.

	w.WriteHeader(http.StatusServiceUnavailable)
	response := m.resolveMessagePlaceholder(serviceUnavailableResponseBytes, err)

	if _, writeErr := w.Write(response); writeErr != nil {
		log.Err(writeErr).Msg("failed to write into http.ResponseWriter")
	}
}

// resolveMessagePlaceholder substitutes ${message} in template with escaped error message.
func (m *TraefikCacheMiddleware) resolveMessagePlaceholder(msg []byte, err error) []byte {
	escaped, _ := json.Marshal(err.Error())
	return bytes.ReplaceAll(msg, messagePlaceholder, escaped[1:len(escaped)-1])
}
