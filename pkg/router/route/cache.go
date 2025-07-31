package route

import (
	"errors"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/header"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/router/counter"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/valyala/fasthttp"
	"net/http"
	"sync/atomic"
	"time"
	"unsafe"
)

var (
	routeNotFoundError                  = errors.New("cache route not found")
	upstreamBadStatusCodeError          = errors.New("upstream bad status code")
	temporaryUnavailableStatusCodeError = errors.New("temporary unavailable status code")
	contentType                         = []byte("application/json")
)

// isCacheEnabled indicates whether the advanced cache is turned on or off.
// It can be safely accessed and modified concurrently.
var isCacheEnabled atomic.Bool

func IsCacheEnabled() bool {
	return isCacheEnabled.Load()
}

func EnableCache() {
	isCacheEnabled.Store(true)
}

func DisableCache() {
	isCacheEnabled.Store(false)
}

type CacheRoute struct {
	cfg      *config.Cache
	storage  storage.Storage
	backend  repository.Backender
	upstream *UpstreamRoute
	rules    map[string]*config.Rule
}

func NewCacheRoutes(cfg *config.Cache, storage storage.Storage, backend repository.Backender) *CacheRoute {
	return &CacheRoute{
		cfg:      cfg,
		storage:  storage,
		backend:  backend,
		upstream: NewUpstream(backend),
		rules:    cfg.Cache.Rules,
	}
}

func (c *CacheRoute) Handle(r *fasthttp.RequestCtx) {
	var from = time.Now()
	defer func() { counter.Duration.Add(time.Since(from).Nanoseconds()) }()
	counter.Total.Add(1)

	r.SetContentTypeBytes(contentType)
	r.SetStatusCode(200)
	r.Write([]byte(`{"status":"ok"}`))
	return

	rule, ok := c.rules[unsafe.String(unsafe.SliceData(r.Path()), len(r.Path()))]
	if !ok {
		//return routeNotFoundError
		c.upstream.Handle(r)
		return
	}

	reqEntry := model.NewEntryFastHttp(rule, r)

	if entry, hit := c.storage.Get(reqEntry); hit {
		counter.Hits.Add(1)
		c.writeResponse(r, entry)
		return
	}

	counter.Misses.Add(1)
	if entry, err := c.fetchUpstream(r, reqEntry); err == nil {
		c.storage.Set(entry)
		c.writeResponse(r, entry)
		return
	}

	counter.Errors.Add(1)
}

func (c *CacheRoute) fetchUpstream(r *fasthttp.RequestCtx, entry *model.Entry) (*model.Entry, error) {
	path := r.Path()
	query := r.QueryArgs().QueryString()
	queryHeaders, queryReleaser := getQueryHeaders(r)
	defer queryReleaser(queryHeaders)

	counter.Proxies.Add(1)
	statusCode, responseHeaders, body, releaser, err := c.backend.Fetch(entry.Rule(), path, query, queryHeaders)
	defer releaser()
	if err != nil {
		return nil, err
	}

	if statusCode == http.StatusServiceUnavailable {
		return nil, temporaryUnavailableStatusCodeError
	} else if statusCode != http.StatusOK {
		return nil, upstreamBadStatusCodeError
	}

	if entry != nil {
		entry.SetPayload(path, query, queryHeaders, responseHeaders, body, statusCode)
		entry.SetRevalidator(c.backend.RevalidatorMaker())
		entry.TouchUpdatedAt()
	}

	return entry, nil
}

func (c *CacheRoute) writeResponse(r *fasthttp.RequestCtx, entry *model.Entry) error {
	_, _, queryHeaders, responseHeaders, responseBody, status, payloadReleaser, err := entry.Payload()
	defer payloadReleaser(queryHeaders, responseHeaders)
	if err != nil {
		return err
	}

	// Write cached headers
	for _, kv := range *responseHeaders {
		r.Response.Header.SetBytesKV(kv[0], kv[1])
	}

	// Last-Modified
	header.SetLastModifiedFastHttp(r, entry)

	// StatusCode-code
	r.Response.Header.SetStatusCode(status)

	// Write a response body
	if _, err = r.Write(responseBody); err != nil {
		return err
	}

	return nil
}

func (c *CacheRoute) Paths() []string {
	paths := make([]string, 0, len(c.rules))
	for path, _ := range c.rules {
		paths = append(paths, path)
	}
	return paths
}

func (c *CacheRoute) IsEnabled() bool {
	return IsCacheEnabled()
}

func (c *CacheRoute) IsInternal() bool {
	return false
}
