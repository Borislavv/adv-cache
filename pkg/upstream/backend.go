package upstream

import (
	"context"
	"crypto/tls"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/valyala/fasthttp"
	"net"
	"net/http"
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
const connsPerHost = 16392

/* -------------------------------------------------------------------------- */
/*                    HTTP/1.1 + opportunistic HTTP/2 client                  */
/* -------------------------------------------------------------------------- */

// httpClient is the work-horse RoundTripper (only: IPV4).
var httpClient = &fasthttp.Client{
	// TCP-pool
	MaxConnsPerHost:     connsPerHost,
	MaxIdleConnDuration: 30 * time.Second,
	MaxConnWaitTimeout:  100 * time.Millisecond,
	// keep-alive and DNS/SYN timeouts
	Dial: func(addr string) (net.Conn, error) {
		return (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial("tcp", addr)
	},
	DialDualStack: false, // use only IPV4
	// TLS-configuration
	TLSConfig: &tls.Config{
		MinVersion:         tls.VersionTLS12, // TLS1.2+ only
		ClientSessionCache: tls.NewLRUClientSessionCache(connsPerHost * 1.5),
	},
	// Read/Write timeouts.
	ReadTimeout:         100 * time.Millisecond, // guard p99
	WriteTimeout:        100 * time.Millisecond,
	MaxResponseBodySize: 0,
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
	Fetch(rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (
		outReq *fasthttp.Request, outResp *fasthttp.Response,
		releaser func(*fasthttp.Request, *fasthttp.Response), err error,
	)
}

type BackendNode struct {
	name      string
	ctx       context.Context
	cfg       *atomic.Pointer[config.Backend]
	transport *http.Transport
}

// NewBackend creates a new instance of BackendNode.
func NewBackend(ctx context.Context, cfg *config.Backend, name string) *BackendNode {
	backend := &BackendNode{
		ctx:  ctx,
		name: name,
		cfg:  &atomic.Pointer[config.Backend]{},
	}
	backend.cfg.Store(cfg)
	return backend
}

// Fetch actually performs the HTTP request to backend and parses the response.
// Note: that rule is optional and if not provided response will not delete unavailable headers for store.
// Also remember that you need provide only one argument: inCtx or inReq.
func (b *BackendNode) Fetch(rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (
	outReq *fasthttp.Request, outResp *fasthttp.Response,
	releaser func(*fasthttp.Request, *fasthttp.Response), err error,
) {
	/**
	 * Load config of current version.
	 */
	cfg := b.cfg.Load()

	/**
	 * Acquire request and response.
	 */
	outReq = fasthttp.AcquireRequest()
	outResp = fasthttp.AcquireResponse()

	/**
	 * Sets upped releaser func.
	 * Note: If you don't know why it is here, read more about sync.Pool, memory management and 'use after free' error.
	 */
	releaser = func(rq *fasthttp.Request, rsp *fasthttp.Response) {
		fasthttp.ReleaseRequest(rq)
		fasthttp.ReleaseResponse(rsp)
	}

	/**
	 * Builds URI (at first COPIES request from source) fur acquired request.
	 */
	if inCtx == nil {
		inReq.CopyTo(outReq)
	} else {
		inCtx.Request.CopyTo(outReq)
	}
	outReq.URI().SetSchemeBytes(cfg.SchemeBytes) // http or https
	outReq.URI().SetHostBytes(cfg.HostBytes)     // backend.example.com

	/**
	 * Determines, whether it should use a regular timeout or the maximum value.
	 */
	var timeout = cfg.Timeout
	if len(outReq.Header.PeekBytes(cfg.UseMaxTimeoutHeaderBytes)) > 0 {
		timeout = cfg.MaxTimeout
	}

	/**
	 * Execute request to an external backend.
	 */
	if err = httpClient.DoTimeout(outReq, outResp, timeout); err != nil {
		releaser(outReq, outResp)
		return nil, nil, emptyReleaserFn, err
	}

	/**
	 * Parse response headers and store only available.
	 */
	if rule != nil {
		allowedHeadersMap := rule.CacheValue.HeadersMap
		outResp.Header.All()(func(k, v []byte) bool {
			if _, ok := allowedHeadersMap[unsafe.String(unsafe.SliceData(k), len(k))]; !ok {
				outResp.Header.DelBytes(k)
			}
			return true
		})
	}

	return outReq, outResp, releaser, nil
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
	if err := httpClient.DoTimeout(req, resp, cfg.Timeout); err != nil {
		return false
	}
	return resp.StatusCode() != fasthttp.StatusOK
}

func (b *BackendNode) Update(cfg *config.Backend) error {
	b.cfg.Store(cfg)
	return nil
}
