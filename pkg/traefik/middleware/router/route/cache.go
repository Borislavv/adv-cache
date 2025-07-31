package route

import (
	"errors"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/header"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/traefik/middleware/router/counter"
	"net/http"
	"sync/atomic"
	"unsafe"
)

var (
	routeNotFoundError                  = errors.New("cache route not found")
	upstreamBadStatusCodeError          = fmt.Errorf("upstream bad status code")
	temporaryUnavailableStatusCodeError = fmt.Errorf("temporaty unavailable status code")
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
	cfg     *config.Cache
	storage storage.Storage
	backend repository.Backender
	rules   map[string]*config.Rule
}

func NewCacheRoutes(cfg *config.Cache, storage storage.Storage, backend repository.Backender) *CacheRoute {
	return &CacheRoute{
		cfg:     cfg,
		storage: storage,
		backend: backend,
		rules:   cfg.Cache.Rules,
	}
}

func (c *CacheRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	rule, ok := c.rules[r.URL.Path]
	if !ok {
		return routeNotFoundError
	}

	reqEntry := model.NewEntryNetHttp(rule, r)

	if entry, hit := c.storage.Get(reqEntry); hit {
		counter.Hits.Add(1)
		return c.writeResponse(w, entry)
	}

	counter.Misses.Add(1)
	if entry, err := c.fetchUpstream(r, reqEntry); err == nil {
		c.storage.Set(entry)
		return c.writeResponse(w, entry)
	} else {
		return err
	}
}

func (c *CacheRoute) fetchUpstream(r *http.Request, entry *model.Entry) (*model.Entry, error) {
	path := unsafe.Slice(unsafe.StringData(r.URL.Path), len(r.URL.Path))
	query := unsafe.Slice(unsafe.StringData(r.URL.RawQuery), len(r.URL.RawQuery))

	queryHeaders, queryReleaser := getQueryHeaders(r)
	defer queryReleaser(queryHeaders)

	var rule *config.Rule
	if entry != nil {
		rule = entry.Rule()
	}

	counter.Proxies.Add(1)
	statusCode, responseHeaders, body, releaser, err := c.backend.Fetch(rule, path, query, queryHeaders)
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

func (c *CacheRoute) writeResponse(w http.ResponseWriter, entry *model.Entry) error {
	_, _, queryHeaders, responseHeaders, responseBody, status, payloadReleaser, err := entry.Payload()
	defer payloadReleaser(queryHeaders, responseHeaders)
	if err != nil {
		fmt.Println("payload error: ", err.Error())
		return err
	}

	// Write cached headers
	for _, kv := range *responseHeaders {
		w.Header().Add(
			unsafe.String(unsafe.SliceData(kv[0]), len(kv[0])),
			unsafe.String(unsafe.SliceData(kv[1]), len(kv[1])),
		)
	}

	// Last-Modified
	header.SetLastModifiedNetHttp(w, entry)

	// StatusCode-code
	w.WriteHeader(status)

	// Content-Type
	w.Header().Set("Content-Type", "application/json")

	// Write a response body
	if _, err = w.Write(responseBody); err != nil {
		fmt.Println("write error: ", err.Error())
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
