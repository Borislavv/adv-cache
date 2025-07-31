package route

import (
	"github.com/Borislavv/advanced-cache/pkg/header"
	"github.com/Borislavv/advanced-cache/pkg/pools"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/router/counter"
	"github.com/valyala/fasthttp"
	"net/http"
	"sync/atomic"
	"time"
)

// isCacheEnabled indicates whether the advanced cache is turned on or off.
// It can be safely accessed and modified concurrently.
var isUpstreamEnabled atomic.Bool

func IsUpstreamEnabled() bool {
	return isUpstreamEnabled.Load()
}

func EnableUpstream() {
	isUpstreamEnabled.Store(true)
}

func DisableUpstream() {
	isUpstreamEnabled.Store(false)
}

type UpstreamRoute struct {
	backend repository.Backender
}

func NewUpstream(backend repository.Backender) *UpstreamRoute {
	return &UpstreamRoute{backend: backend}
}

func (u *UpstreamRoute) Handle(r *fasthttp.RequestCtx) error {
	counter.Proxies.Add(1)

	status, headers, body, releaser, err := u.fetchUpstream(r)
	defer releaser()
	if err != nil {
		return err
	}

	if err = u.writeResponse(r, status, headers, body); err != nil {
		return err
	}

	return nil
}

func (u *UpstreamRoute) fetchUpstream(r *fasthttp.RequestCtx) (status int, headers *[][2][]byte, body []byte, releaser func(), err error) {
	path := r.Path()
	query := r.QueryArgs().QueryString()
	queryHeaders, queryReleaser := getQueryHeaders(r)
	defer queryReleaser(queryHeaders)

	counter.Proxies.Add(1)
	status, headers, body, releaser, err = u.backend.Fetch(nil, path, query, queryHeaders)
	if err != nil {
		return status, headers, body, releaser, err
	}

	if status == http.StatusServiceUnavailable {
		releaser()
		return status, headers, body, releaser, err
	} else if status != http.StatusOK {
		return status, headers, body, releaser, err
	}

	return status, headers, body, releaser, err
}

func (u *UpstreamRoute) writeResponse(r *fasthttp.RequestCtx, status int, headers *[][2][]byte, body []byte) error {
	// Write cached headers
	for _, kv := range *headers {
		r.Response.Header.SetBytesKV(kv[0], kv[1])
	}

	// Last-Modified
	header.SetLastModifiedValueFastHttp(r, time.Now().UnixNano())

	// StatusCode-code
	r.SetStatusCode(status)

	// Write a response body
	_, err := r.Write(body)
	return err
}

func (u *UpstreamRoute) Paths() []string {
	return nil
}

func (u *UpstreamRoute) IsEnabled() bool {
	return IsUpstreamEnabled()
}

func (u *UpstreamRoute) IsInternal() bool {
	return false
}

var (
	// if you return a releaser as an outer variable it will not allocate closure each time on call function
	queryHeadersReleaser = func(headers *[][2][]byte) {
		*headers = (*headers)[:0]
		pools.KeyValueSlicePool.Put(headers)
	}
)

func getQueryHeaders(r *fasthttp.RequestCtx) (headers *[][2][]byte, releaseFn func(*[][2][]byte)) {
	headers = pools.KeyValueSlicePool.Get().(*[][2][]byte)
	r.Request.Header.All()(func(key []byte, value []byte) bool {
		*headers = append(*headers, [2][]byte{key, value})
		return true
	})
	return headers, queryHeadersReleaser
}
