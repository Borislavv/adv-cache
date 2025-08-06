package upstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/pools"
	"github.com/valyala/fasthttp"
	"golang.org/x/time/rate"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var healthCheckPath = []byte("/k8s/probe")

var transport = &http.Transport{
	// Max idle (keep-alive) connections across ALL hosts
	MaxIdleConns: 10000,

	// Max idle (keep-alive) connections per host
	MaxIdleConnsPerHost: 1000,

	// Max concurrent connections per host (optional)
	MaxConnsPerHost: 0, // 0 = unlimited (use with caution)

	IdleConnTimeout: 30 * time.Second,

	// Optional: tune dialer
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,

	DialTLS: func(network, addr string) (net.Conn, error) {
		return tls.Dial("tcp", addr, &tls.Config{
			InsecureSkipVerify: true, // или false — если нужен real cert check
		})
	},

	// Optional: configure TLS handshake timeout, etc.
	TLSHandshakeTimeout: 10 * time.Second,

	// ExpectContinueTimeout: wait time for 100-continue
	ExpectContinueTimeout: 1 * time.Second,
}

// -----------------------------------------------------------------------------
// Backend contract (only what the cluster really needs).
// -----------------------------------------------------------------------------

type fetchFn = func(rule *config.Rule, path, query []byte, queryHeaders *[][2][]byte) (
	status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
)

// Backend is the subset of methods the balancer depends on. Methods must backend
// thread‑safe.
//
// Imposing a very small surface keeps the cluster independent from the concrete
// backend implementation.
//
//nolint:interfacebloat // explicit on purpose
type Backend interface {
	Name() string
	IsHealthy() bool
	Update(cfg *config.Backend) error
	Fetch(rule *config.Rule, path, query []byte, queryHeaders *[][2][]byte) (
		status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
	)
}

type BackendNode struct {
	name        string
	ctx         context.Context
	cfg         *atomic.Pointer[config.Backend]
	transport   *http.Transport
	clientsPool *sync.Pool
	rateLimiter *rate.Limiter
}

// NewBackend creates a new instance of BackendNode.
func NewBackend(ctx context.Context, cfg *config.Backend, name string) *BackendNode {
	backendRate := cfg.Rate
	backendRateBurst := backendRate / 30
	if backendRateBurst <= 1 {
		backendRateBurst = 1
	}

	backend := &BackendNode{
		ctx:  ctx,
		name: name,
		clientsPool: &sync.Pool{
			New: func() interface{} {
				return &http.Client{
					Transport: transport,
					Timeout:   10 * time.Second,
				}
			},
		},
		rateLimiter: rate.NewLimiter(
			rate.Limit(backendRate),
			backendRateBurst,
		),
	}

	backend.cfg.Store(cfg)

	return backend
}

func (b *BackendNode) Update(cfg *config.Backend) error {
	b.cfg.Store(cfg)
	return nil
}

func (b *BackendNode) Fetch(
	rule *config.Rule, path []byte, query []byte, queryHeaders *[][2][]byte,
) (
	status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
) {
	if err = b.rateLimiter.Wait(b.ctx); err != nil {
		return 0, nil, nil, emptyReleaseFn, err
	}

	return b.requestExternalBackend(rule, path, query, queryHeaders)
}

// MakeRevalidator builds a new revalidator for model.Response by catching a request into closure for backend able to call backend later.
func (b *BackendNode) MakeRevalidator() func(
	rule *config.Rule, path []byte, query []byte, queryHeaders *[][2][]byte,
) (
	status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
) {
	return func(
		rule *config.Rule, path []byte, query []byte, queryHeaders *[][2][]byte,
	) (
		status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
	) {
		return b.requestExternalBackend(rule, path, query, queryHeaders)
	}
}

var (
	emptyReleaseFn = func() {}
	urlBufPool     = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
	queryPrefix = []byte("?")
)

// requestExternalBackend actually performs the HTTP request to backend and parses the response.
func (b *BackendNode) requestExternalBackend(
	rule *config.Rule, path []byte, query []byte, queryHeaders *[][2][]byte,
) (status int, headers *[][2][]byte, body []byte, releaseFn func(), err error) {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	urlBuf := urlBufPool.Get().(*bytes.Buffer)
	defer func() { urlBuf.Reset(); urlBufPool.Put(urlBuf) }()

	cfg := b.cfg.Load()

	req.Header.SetMethod(fasthttp.MethodGet)
	urlBuf.Grow(len(cfg.UrlBytes) + len(path) + len(query) + 1)

	if _, err = urlBuf.Write(cfg.UrlBytes); err != nil {
		return 0, nil, nil, emptyReleaseFn, err
	}
	if _, err = urlBuf.Write(path); err != nil {
		return 0, nil, nil, emptyReleaseFn, err
	}
	if _, err = urlBuf.Write(queryPrefix); err != nil {
		return 0, nil, nil, emptyReleaseFn, err
	}
	if _, err = urlBuf.Write(query); err != nil {
		return 0, nil, nil, emptyReleaseFn, err
	}
	req.SetRequestURIBytes(urlBuf.Bytes())

	var isMaxTimeoutUsageAllowed bool
	for _, kv := range *queryHeaders {
		req.Header.SetBytesKV(kv[0], kv[1])
		if bytes.Equal(kv[0], cfg.UseMaxTimeoutHeaderBytes) {
			isMaxTimeoutUsageAllowed = true
		}
	}

	var timeout time.Duration
	if isMaxTimeoutUsageAllowed {
		timeout = cfg.MaxTimeout
	} else {
		timeout = cfg.Timeout
	}

	resp := fasthttp.AcquireResponse()
	if err = pools.BackendHttpClientPool.DoTimeout(req, resp, timeout); err != nil {
		fasthttp.ReleaseResponse(resp)
		return 0, nil, nil, emptyReleaseFn, err
	}

	headers = pools.KeyValueSlicePool.Get().(*[][2][]byte)

	if rule != nil {
		allowedHeadersMap := rule.CacheValue.HeadersMap
		resp.Header.VisitAll(func(k, v []byte) {
			if _, ok := allowedHeadersMap[unsafe.String(unsafe.SliceData(k), len(k))]; ok {
				*headers = append(*headers, [2][]byte{k, v})
			}
		})
	} else {
		resp.Header.VisitAll(func(k, v []byte) {
			*headers = append(*headers, [2][]byte{k, v})
		})
	}

	buf := pools.BackendBodyBufferPool.Get().(*bytes.Buffer)

	// make a final releaser func
	releaseFn = func() {
		*headers = (*headers)[:0]
		pools.KeyValueSlicePool.Put(headers)

		buf.Reset()
		pools.BackendBodyBufferPool.Put(buf)

		fasthttp.ReleaseResponse(resp)
	}

	if err = resp.BodyWriteTo(buf); err != nil {
		releaseFn() // release on error
		return 0, nil, nil, emptyReleaseFn, err
	}

	return resp.StatusCode(), headers, buf.Bytes(), releaseFn, nil
}

func (b *BackendNode) Name() string {
	return b.name
}

func (b *BackendNode) IsHealthy() bool {
	cfg := b.cfg.Load()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	urlBuf := urlBufPool.Get().(*bytes.Buffer)
	defer func() { urlBuf.Reset(); urlBufPool.Put(urlBuf) }()

	req.Header.SetMethod(fasthttp.MethodGet)
	urlBuf.Grow(len(cfg.UrlBytes) + len(healthCheckPath))

	if _, err := urlBuf.Write(cfg.UrlBytes); err != nil {
		return false
	}
	if _, err := urlBuf.Write(healthCheckPath); err != nil {
		return false
	}
	req.SetRequestURIBytes(urlBuf.Bytes())

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	if err := pools.BackendHttpClientPool.DoTimeout(req, resp, cfg.Timeout); err != nil {
		return false
	}
	statusCode := resp.StatusCode()

	return statusCode != fasthttp.StatusOK
}
