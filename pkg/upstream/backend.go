package upstream

import (
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/pools"
	"github.com/valyala/fasthttp"
	"net"
	"net/http"
	"time"
	"unsafe"
)

var (
	defaultReleaser         = func(*fasthttp.Request, *fasthttp.Response) {}
	ErrNotHealthyStatusCode = errors.New("bad status code")
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
	MaxIdleConnDuration: 60 * time.Second,
	MaxConnWaitTimeout:  500 * time.Millisecond,
	// keep-alive and DNS/SYN timeouts
	Dial: func(addr string) (net.Conn, error) {
		return (&net.Dialer{
			Timeout:   10 * time.Second,
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
	ReadTimeout:         500 * time.Millisecond,
	WriteTimeout:        500 * time.Millisecond,
	ReadBufferSize:      32784,
	WriteBufferSize:     32784,
	MaxResponseBodySize: 0,
}

type Backend interface {
	ID() string
	Name() string
	IsHealthy() error
	Cfg() *config.Backend
	Fetch(rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (
		outReq *fasthttp.Request, outResp *fasthttp.Response,
		releaser func(*fasthttp.Request, *fasthttp.Response), err error,
	)
}

type BackendNode struct {
	id        string
	cfg       *config.Backend
	transport *http.Transport
}

// NewBackend creates a new instance of BackendNode.
func NewBackend(cfg *config.Backend) *BackendNode {
	return &BackendNode{id: cfg.ID, cfg: cfg}
}

// Fetch actually performs the HTTP request to backend and parses the response.
// Note: that rule is optional and if not provided response will not delete unavailable headers for store.
// Also remember that you need provide only one argument: inCtx or inReq.
func (b *BackendNode) Fetch(rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (
	outReq *fasthttp.Request, outResp *fasthttp.Response, releaser func(*fasthttp.Request, *fasthttp.Response), err error,
) {
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
	outReq.URI().SetSchemeBytes(b.cfg.SchemeBytes) // http or https
	outReq.URI().SetHostBytes(b.cfg.HostBytes)     // backend.example.com

	/**
	 * Determines, whether it should use a regular timeout or the maximum value.
	 */
	var timeout = b.cfg.Timeout
	if len(outReq.Header.PeekBytes(b.cfg.UseMaxTimeoutHeaderBytes)) > 0 {
		timeout = b.cfg.MaxTimeout
	}

	/**
	 * Execute request to an external backend.
	 */
	if err = httpClient.DoTimeout(outReq, outResp, timeout); err != nil {
		releaser(outReq, outResp)
		return nil, nil, defaultReleaser, err
	}

	/**
	 * Filters response headers if rule !== nil (mutates response).
	 */
	b.filterHeaders(rule, outResp)

	/**
	 * Respond with non-default releaser or outReq and outResp will be leaked from fasthttp internal pools!
	 */
	return outReq, outResp, releaser, nil
}

func (b *BackendNode) filterHeaders(rule *config.Rule, resp *fasthttp.Response) {
	if rule != nil {
		allowedMap := rule.CacheValue.HeadersMap
		if len(allowedMap) > 0 {
			var kvBuf = pools.KeyValueSlicePool.Get().(*[][2][]byte)

			resp.Header.All()(func(k, v []byte) bool {
				if _, ok := allowedMap[unsafe.String(unsafe.SliceData(k), len(k))]; ok {
					*kvBuf = append(*kvBuf, [2][]byte{k, v})
				}
				return true
			})

			resp.Header.Reset()
			for _, kv := range *kvBuf {
				resp.Header.AddBytesKV(kv[0], kv[1])
			}

			*kvBuf = (*kvBuf)[:0]
			pools.KeyValueSlicePool.Put(kvBuf)
		}
	}
}

func (b *BackendNode) ID() string {
	return b.id
}

func (b *BackendNode) Name() string {
	return fmt.Sprintf("%s (%s://%s)", b.id, b.cfg.Scheme, b.cfg.Host)
}

func (b *BackendNode) Cfg() *config.Backend {
	return b.cfg
}

func (b *BackendNode) IsHealthy() error {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	uri := req.URI()
	uri.SetSchemeBytes(b.cfg.SchemeBytes)    // "http" / "https"
	uri.SetHostBytes(b.cfg.HostBytes)        // backend.example.com
	uri.SetPathBytes(b.cfg.HealthcheckBytes) // /k8s/probe or /healthcheck for example

	req.Header.SetMethod(fasthttp.MethodGet)
	if err := httpClient.DoTimeout(req, resp, b.cfg.Timeout); err != nil {
		return err
	}
	if resp.StatusCode() != fasthttp.StatusOK {
		return ErrNotHealthyStatusCode
	}
	return nil
}
