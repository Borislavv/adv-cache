package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"
)

var (
	contentTypeKey       = "Content-Type"
	applicationJsonValue = "application/json"
	lastModifiedKey      = "Last-Modified"
)

type TraefikCacheMiddleware struct {
	ctx       context.Context
	next      http.Handler
	name      string
	cfg       *config.Cache
	store     storage.Storage
	backend   repository.Backender
	refresher storage.Refresher
	evictor   storage.Evictor
	dumper    storage.Dumper
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

func (middleware *TraefikCacheMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer runtime.Gosched()

	from := time.Now()

	// Build request (return error on rule missing for current path)
	req, err := model.NewRequestFromNetHttp(middleware.cfg, r)
	if err != nil {
		// If path does not match then process request manually without cache
		middleware.next.ServeHTTP(w, r)
		return
	}

	resp, isHit := middleware.store.Get(req)
	if !isHit {
		captured := newCaptureResponseWriter(w)

		// Handle request manually due to store it
		middleware.next.ServeHTTP(captured, r)

		// Build new response
		data := model.NewData(req.Rule(), captured.statusCode, captured.headers, captured.body.Bytes())
		resp, _ = model.NewResponse(data, req, middleware.cfg, middleware.backend.RevalidatorMaker(req))

		// Store response in cache
		middleware.store.Set(resp)
	} else {
		// Write status code on hit
		w.WriteHeader(resp.Data().StatusCode())

		// Write response data
		_, _ = w.Write(resp.Data().Body())

		// Apply custom http headers
		for key, vv := range resp.Data().Headers() {
			for _, value := range vv {
				w.Header().Add(key, value)
			}
		}
	}

	// Apply standard http headers
	w.Header().Add(contentTypeKey, applicationJsonValue)
	w.Header().Add(lastModifiedKey, resp.RevalidatedAt().Format(http.TimeFormat))

	// Record the duration in debug mode for metrics.
	atomic.AddInt64(&middleware.count, 1)
	atomic.AddInt64(&middleware.duration, time.Since(from).Nanoseconds())
}
