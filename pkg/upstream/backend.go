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
	Fetch(rule *config.Rule, ctx *fasthttp.RequestCtx) (
		path, query []byte, qHeaders, rHeaders *[][2][]byte,
		status int, body []byte, releaseFn func(), err error,
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

func (b *BackendNode) FetchFasthttp(rule *config.Rule, ctx *fasthttp.RequestCtx) (
	path, query []byte, qHeaders, rHeaders *[][2][]byte,
	status int, body []byte, releaseFn func(), err error,
) {
	if err = b.rateLimiter.Wait(b.ctx); err != nil {
		return nil, nil, nil, nil, 0, nil, emptyReleaseFn, err
	}
	return b.requestExternalBackend(rule, ctx)
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
	rule *config.Rule, ctx *fasthttp.RequestCtx,
) (
	path, query []byte, qHeaders, rHeaders *[][2][]byte,
	status int, body []byte, releaser func(q, h *[][2][]byte), err error, TODO stopped here -> (q, h
) {
	cfg := b.cfg.Load()
	path = ctx.Path()
	query = ctx.QueryArgs().QueryString()

	/**
	 * Acquire request.
	 */
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.Header.SetMethod(fasthttp.MethodGet)

	/**
	 * Builds URL and return it in pool immediately due to it already set upped into request.
	 */
	urlLen := len(cfg.UrlBytes) + len(path) + len(query) + 1
	urlBuf := urlBufPool.Get().(*[]byte)
	if len(*urlBuf) < urlLen {
		*urlBuf = make([]byte, 0, urlLen)
	}
	*urlBuf = append(*urlBuf, cfg.UrlBytes...)
	*urlBuf = append(*urlBuf, path...)
	*urlBuf = append(*urlBuf, queryPrefix...)
	*urlBuf = append(*urlBuf, query...)
	req.SetRequestURIBytes(req.URI().FullURI())
	urlBufPool.Put((*urlBuf)[:0])

	/**
	 * 1. Acquire and parse query headers.
	 * 2. Determines, should he use a regular timeout or the maximum value.
	 * 3. Sets query headers to new request.
	 */
	var timeout = cfg.Timeout
	*qHeaders = (*(pools.KeyValueSlicePool.Get().(*[][2][]byte)))[:0]
	ctx.Request.Header.All()(func(key []byte, value []byte) bool {
		req.Header.SetBytesKV(key, value)
		*qHeaders = append(*qHeaders, [2][]byte{key, value})
		if bytes.Equal(key, cfg.UseMaxTimeoutHeaderBytes) {
			timeout = cfg.MaxTimeout
		}
		return true
	})

	/**
	 * Acquire and execute request, set status code value.
	 */
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)
	if err = pools.BackendHttpClientPool.DoTimeout(req, resp, timeout); err != nil {
		pools.KeyValueSlicePool.Put((*qHeaders)[:0])
		status = resp.StatusCode()
		return
	} else {
		status = resp.StatusCode()
	}

	/**
	 * Parse response headers and store only available.
	 */
	*rHeaders = (*(pools.KeyValueSlicePool.Get().(*[][2][]byte)))[:0]
	if rule != nil {
		allowedHeadersMap := rule.CacheValue.HeadersMap
		resp.Header.All()(func(k, v []byte) bool {
			if _, ok := allowedHeadersMap[unsafe.String(unsafe.SliceData(k), len(k))]; ok {
				*rHeaders = append(*rHeaders, [2][]byte{k, v})
			}
			return true
		})
	} else {
		resp.Header.All()(func(k, v []byte) bool {
			*rHeaders = append(*rHeaders, [2][]byte{k, v})
			return true
		})
	}

	/**
	 * Acquire buffer for read body into.
	 */
	buf := pools.BackendBodyBufferPool.Get().(*bytes.Buffer)

	/**
	 * Sets upped releaser func.
	 * If you don't know why it is here, read more about sync.Pool, memory management and 'use after free' error.
	 */
	releaser = func(qHeaders, rHeaders *[][2][]byte) {
		buf.Reset()
		pools.BackendBodyBufferPool.Put(buf)
		pools.KeyValueSlicePool.Put((*qHeaders)[:0])
		pools.KeyValueSlicePool.Put((*rHeaders)[:0])
	}

	if err = resp.BodyWriteTo(buf); err != nil {
		releaser()
		return
	} else {
		body = buf.Bytes()
	}

	return
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

var (
	// if you return a releaser as an outer variable it will not allocate closure each time on call function
	queryHeadersReleaser = func(headers *[][2][]byte) {
		*headers = (*headers)[:0]
		pools.KeyValueSlicePool.Put(headers)
	}
)

func parseQueryHeaders(r *fasthttp.RequestCtx) (headers *[][2][]byte, releaseFn func(*[][2][]byte)) {

}
