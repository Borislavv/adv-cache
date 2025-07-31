package route

import (
	"github.com/Borislavv/advanced-cache/pkg/header"
	"github.com/Borislavv/advanced-cache/pkg/pools"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/traefik/middleware/router/counter"
	"net/http"
	"sync/atomic"
	"time"
	"unsafe"
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

func (u *UpstreamRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	counter.Proxies.Add(1)

	status, headers, body, releaser, err := u.fetchUpstream(r)
	defer releaser()
	if err != nil {
		return err
	}

	if err = u.writeResponse(w, status, headers, body); err != nil {
		return err
	}

	return nil
}

func (u *UpstreamRoute) fetchUpstream(r *http.Request) (status int, headers *[][2][]byte, body []byte, releaser func(), err error) {
	path := unsafe.Slice(unsafe.StringData(r.URL.Path), len(r.URL.Path))
	query := unsafe.Slice(unsafe.StringData(r.URL.RawQuery), len(r.URL.RawQuery))

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

func (u *UpstreamRoute) writeResponse(w http.ResponseWriter, status int, headers *[][2][]byte, body []byte) error {
	// Write cached headers
	for _, kv := range *headers {
		w.Header().Add(
			unsafe.String(unsafe.SliceData(kv[0]), len(kv[0])),
			unsafe.String(unsafe.SliceData(kv[1]), len(kv[1])),
		)
	}

	// Last-Modified
	header.SetLastModifiedValueNetHttp(w, time.Now().UnixNano())

	// StatusCode-code
	w.WriteHeader(status)

	// Content-Type
	w.Header().Set("Content-Type", "application/json")

	// Write a response body
	_, err := w.Write(body)
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

func getQueryHeaders(r *http.Request) (headers *[][2][]byte, releaseFn func(*[][2][]byte)) {
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
