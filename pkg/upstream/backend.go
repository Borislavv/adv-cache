package upstream

import (
	"context"
	"crypto/tls"
	bytes2 "github.com/Borislavv/advanced-cache/pkg/bytes"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/pools"
	"github.com/valyala/fasthttp"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var (
	healthCheckPath = []byte("/k8s/probe")
	emptyReleaserFn = func(*fasthttp.Request, *fasthttp.Response) {}
)

/* -------------------------------------------------------------------------- */
/*                              shared constants                              */
/* -------------------------------------------------------------------------- */

// connsPerHost:
//   - With HTTP/1.1 each in-flight request occupies one TCP connection.
//   - We expect ≈ 5 k concurrent requests (500 k RPS × 10 ms p99 RTT).
//   - ×2 head-room gives 10 k.

// idleExtra:
//   - A small cushion so bursts don’t immediately allocate new sockets.

// headerLimit:
//   - Protects against “run-away” Set-Cookie explosions; 32 KiB is plenty
//     for 99 % of real-world APIs.
const (
	connsPerHost = 10_000
	idleExtra    = 6_000
	headerLimit  = 32 << 10 // 32 KiB
)

/* -------------------------------------------------------------------------- */
/*                    HTTP/1.1 + opportunistic HTTP/2 client                  */
/* -------------------------------------------------------------------------- */

// transport is the work-horse RoundTripper. It automatically speaks
// HTTP/2 when the origin advertises support via ALPN, otherwise falls back
// to HTTP/1.1. All knobs are biased toward high throughput on a 64-CPU box.
var transport = &http.Transport{
	/* connection pool */
	MaxConnsPerHost:     connsPerHost,             // hard cap on active TCP sockets
	MaxIdleConnsPerHost: connsPerHost,             // keep them all warm
	MaxIdleConns:        connsPerHost + idleExtra, // allow bursts
	IdleConnTimeout:     30 * time.Second,         // lower to free idle sockets sooner

	/* disable proxy lookups (no ProxyFromEnvironment) */
	Proxy: nil,

	/* dialer: quick fail on DNS/SYN hiccups */
	DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,

	/* TLS — tuned for low handshake latency */
	TLSHandshakeTimeout: 3 * time.Second,
	TLSClientConfig: &tls.Config{
		MinVersion:         tls.VersionTLS12,                     // TLS 1.2+ only
		ClientSessionCache: tls.NewLRUClientSessionCache(16_384), // larger cache for >10k conns
	},

	/* request/response timeouts & limits */
	ResponseHeaderTimeout:  100 * time.Millisecond, // stricter tail-latency guard
	ExpectContinueTimeout:  0,                      // no 100-continue for small requests
	MaxResponseHeaderBytes: headerLimit,
	DisableCompression:     true, // backend already gzips

	/* not let the stdlib attempt HTTP/2 automatically */
	ForceAttemptHTTP2: false,
}

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
	Cfg() *config.Backend
	Update(cfg *config.Backend) error
	Fetch(rule *config.Rule, ctx *fasthttp.RequestCtx) (
		req *fasthttp.Request, resp *fasthttp.Response,
		releaser func(*fasthttp.Request, *fasthttp.Response), err error,
	)
}

type BackendNode struct {
	name        string
	ctx         context.Context
	cfg         *atomic.Pointer[config.Backend]
	transport   *http.Transport
	clientsPool *sync.Pool
}

// NewBackend creates a new instance of BackendNode.
func NewBackend(ctx context.Context, cfg *config.Backend, name string) *BackendNode {
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
	}
	backend.cfg.Store(cfg)
	return backend
}

// Fetch actually performs the HTTP request to backend and parses the response.
// Note: that rule is optional and if not provided response will not delete unavailable headers for store.
func (b *BackendNode) Fetch(rule *config.Rule, ctx *fasthttp.RequestCtx) (
	req *fasthttp.Request, resp *fasthttp.Response,
	releaser func(*fasthttp.Request, *fasthttp.Response), err error,
) {
	/**
	 * Load config of current version.
	 */
	cfg := b.cfg.Load()

	/**
	 * Acquire request and response.
	 */
	req = fasthttp.AcquireRequest()
	resp = fasthttp.AcquireResponse()

	/**
	 * Sets upped releaser func.
	 * Note: If you don't know why it is here, read more about sync.Pool, memory management and 'use after free' error.
	 */
	releaser = func(rq *fasthttp.Request, rsp *fasthttp.Response) {
		fasthttp.ReleaseRequest(rq)
		fasthttp.ReleaseResponse(rsp)
	}

	/**
	 * Builds URI of acquire request.
	 */
	uri := req.URI()
	uri.SetSchemeBytes(cfg.SchemeBytes) // http or https
	uri.SetHostBytes(cfg.HostBytes)     // backend.example.com
	uri.SetPathBytes(ctx.Path())        // /api/v1/get/data
	if ctx.QueryArgs().Len() > 0 {
		uri.SetQueryStringBytes(ctx.QueryArgs().QueryString()) // bar=baz&foo=buz
	}

	/**
	 * 1. Acquire and parse query headers.
	 * 2. Determine, should it use a regular timeout or the maximum value.
	 * 3. Set query headers to new request.
	 */
	var timeout = cfg.Timeout
	ctx.Request.Header.All()(func(key []byte, value []byte) bool {
		req.Header.SetBytesKV(key, value)
		// check whether the request has escape timeout header
		if bytes2.IsBytesAreEquals(key, cfg.UseMaxTimeoutHeaderBytes) {
			timeout = cfg.MaxTimeout
		}
		return true
	})

	/**
	 * Execute GET requests to external backend.
	 */
	req.Header.SetMethod(fasthttp.MethodGet)
	if err = pools.BackendHttpClientPool.DoTimeout(req, resp, timeout); err != nil {
		releaser(req, resp)
		return nil, nil, emptyReleaserFn, err
	}

	/**
	 * Parse response headers and store only available.
	 */
	if rule != nil {
		allowedHeadersMap := rule.CacheValue.HeadersMap
		resp.Header.All()(func(k, v []byte) bool {
			if _, ok := allowedHeadersMap[unsafe.String(unsafe.SliceData(k), len(k))]; !ok {
				resp.Header.DelBytes(k)
			}
			return true
		})
	}

	return req, resp, releaser, nil
}

func (b *BackendNode) Name() string {
	return b.name
}

func (b *BackendNode) Cfg() *config.Backend {
	return b.cfg.Load()
}

func (b *BackendNode) IsHealthy() bool {
	cfg := b.cfg.Load()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	uri := req.URI()
	uri.SetSchemeBytes(cfg.SchemeBytes) // "http" / "https"
	uri.SetHostBytes(cfg.HostBytes)     // backend.example.com
	uri.SetPathBytes(healthCheckPath)   // /k8s/probe

	req.Header.SetMethod(fasthttp.MethodGet)
	if err := pools.BackendHttpClientPool.DoTimeout(req, resp, cfg.Timeout); err != nil {
		return false
	}
	return resp.StatusCode() != fasthttp.StatusOK
}

func (b *BackendNode) Update(cfg *config.Backend) error {
	b.cfg.Store(cfg)
	return nil
}
