package advancedcachemiddleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/header"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
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

type TraefikIntermediateConfig struct {
	ConfigPath string `yaml:"configPath" mapstructure:"configPath"`
}

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

type AdvancedCacheMiddleware struct {
	ctx       context.Context
	next      http.Handler
	name      string
	cfg       *config.Cache
	storage   storage.Storage
	backend   repository.Backender
	refresher storage.Refresher
	evictor   storage.Evictor
	metrics   metrics.Meter
	count     int64 // Num
	duration  int64 // UnixNano
}

func NewAdvancedCache(ctx context.Context, next http.Handler, config *TraefikIntermediateConfig, name string) http.Handler {
	cacheMiddleware := &AdvancedCacheMiddleware{
		ctx:  ctx,
		next: next,
		name: name,
	}

	if err := cacheMiddleware.run(ctx, config); err != nil {
		panic(err)
	}

	return cacheMiddleware
}

func (m *AdvancedCacheMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var from = time.Now()
	defer func() { totalDuration.Add(time.Since(from).Nanoseconds()) }()

	total.Add(1)
	if enabled.Load() {
		m.handleThroughCache(w, r)
	} else {
		m.handleThroughProxy(w, r)
	}
}

func (m *AdvancedCacheMiddleware) handleThroughProxy(w http.ResponseWriter, r *http.Request) {
	m.next.ServeHTTP(w, r)
}

func (m *AdvancedCacheMiddleware) handleThroughCache(w http.ResponseWriter, r *http.Request) {
	newEntry, err := model.NewEntryNetHttp(m.cfg, r)
	if err != nil {
		if model.IsRouteWasNotFound(err) {
			m.handleThroughProxy(w, r)
			return
		}
		// Path was not matched, then handle request through upstream without cache.
		m.next.ServeHTTP(w, r)
		return
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

		// Run downstream handler
		m.next.ServeHTTP(captured, r)

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
			// non-positive responseStatus code received, skip saving
			defer newEntry.Remove()

			responseLastModified = time.Now().Unix()
		} else {
			// Save the response into the new newEntry
			newEntry.SetPayload(path, query, queryHeaders, responseHeaders, responseBody, responseStatus)
			newEntry.SetRevalidator(m.backend.RevalidatorMaker())

			// build and store new Entry in cache
			var wasPersisted bool
			if cacheEntry, wasPersisted = m.storage.Set(model.NewVersionPointer(newEntry)); wasPersisted {
				defer cacheEntry.Release() // an Entry stored in the cache, must be released after use
			} else {
				defer cacheEntry.Remove() // an Entry was not persisted, must be removed after use
			}

			responseLastModified = cacheEntry.UpdateAt()
		}
	} else {
		hits.Add(1)

		// deferred release and remove
		newEntry.Remove()          // new Entry which was used as request for query cache does not need anymore
		defer cacheEntry.Release() // an Entry retrieved from the cache must be released after use

		// Always read from cached cacheEntry
		var queryHeaders *[][2][]byte
		var payloadReleaser func(q, h *[][2][]byte)
		_, _, queryHeaders, responseHeaders, responseBody, responseStatus, payloadReleaser, err = cacheEntry.Payload()
		defer payloadReleaser(queryHeaders, responseHeaders)
		if err != nil {
			// this case must not happen, in this case write log
			log.Error().Err(err).Msg("[cache-controller] failed to unpack payload")

			errors.Add(1)

			// ERROR — prepare capture writer
			captured, releaseCapturer := httpwriter.NewCaptureResponseWriter(w)
			defer releaseCapturer()

			m.next.ServeHTTP(captured, r)

			var extractReleaser func(*[][2][]byte)
			responseStatus, responseHeaders, responseBody, extractReleaser = captured.ExtractPayload()
			defer extractReleaser(responseHeaders)
		}

		responseLastModified = cacheEntry.UpdateAt()
	}

	// Write cached responseHeaders
	for _, kv := range *responseHeaders {
		w.Header().Add(
			// it's already safe due to no one changes key and value slices (string will stay immutable)
			unsafe.String(unsafe.SliceData(kv[0]), len(kv[0])),
			unsafe.String(unsafe.SliceData(kv[1]), len(kv[1])),
		)
	}

	// Last-Modified
	lastModifiedBuffer, lastModifiedReleaser := header.SetLastModifiedValueNetHttp(w, responseLastModified)
	defer lastModifiedReleaser(lastModifiedBuffer) // it's necessary due to inside an unsafe cast from []byte to string,
	// so we need hold the origin []byte until the request will end

	// Content-Type
	w.Header().Set(contentTypeKey, applicationJsonValue)

	// Write a response responseBody
	if _, err = w.Write(responseBody); err != nil {
		responseStatus = http.StatusServiceUnavailable
	}

	w.WriteHeader(responseStatus)
}
