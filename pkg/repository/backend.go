package repository

import (
	"bytes"
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

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

	// Optional: configure TLS handshake timeout, etc.
	TLSHandshakeTimeout: 10 * time.Second,

	// ExpectContinueTimeout: wait time for 100-continue
	ExpectContinueTimeout: 1 * time.Second,
}

// Backender defines the interface for a repository that provides SEO page data.
type Backender interface {
	Fetch(ctx context.Context, path []byte, query []byte, queryHeaders [][2][]byte) (status int, headers http.Header, body []byte, err error)
	RevalidatorMaker() func(ctx context.Context, path []byte, query []byte, queryHeaders [][2][]byte) (status int, headers http.Header, body []byte, err error)
}

// Backend implements the Backender interface.
// It fetches and constructs SEO page data responses from an external backend.
type Backend struct {
	cfg         *config.Cache // Global configuration (backend URL, etc)
	transport   *http.Transport
	clientsPool *sync.Pool
}

// NewBackend creates a new instance of Backend.
func NewBackend(cfg *config.Cache) *Backend {
	return &Backend{cfg: cfg, clientsPool: &sync.Pool{
		New: func() interface{} {
			return &http.Client{
				Transport: transport,
				Timeout:   10 * time.Second,
			}
		},
	}}
}

func (s *Backend) Fetch(ctx context.Context, path []byte, query []byte, queryHeaders [][2][]byte) (status int, headers http.Header, body []byte, err error) {
	return s.requestExternalBackend(ctx, path, query, queryHeaders)
}

// RevalidatorMaker builds a new revalidator for model.Response by catching a request into closure for be able to call backend later.
func (s *Backend) RevalidatorMaker() func(ctx context.Context, path []byte, query []byte, queryHeaders [][2][]byte) (status int, headers http.Header, body []byte, err error) {
	return func(ctx context.Context, path []byte, query []byte, queryHeaders [][2][]byte) (status int, headers http.Header, body []byte, err error) {
		return s.requestExternalBackend(ctx, path, query, queryHeaders)
	}
}

// requestExternalBackend actually performs the HTTP request to backend and parses the response.
// Returns a Data object suitable for caching.
func (s *Backend) requestExternalBackend(ctx context.Context, path []byte, query []byte, queryHeaders [][2][]byte) (status int, headers http.Header, body []byte, err error) {
	// Apply a hard timeout for the HTTP request.
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Cache.Upstream.Timeout)
	defer cancel()

	url := s.cfg.Cache.Upstream.Url

	// Efficiently concatenate base URL and query.
	queryBuf := make([]byte, 0, len(url)+len(path)+len(query))
	queryBuf = append(queryBuf, url...)
	queryBuf = append(queryBuf, path...)
	queryBuf = append(queryBuf, query...)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, string(queryBuf), nil)
	if err != nil {
		return 0, nil, nil, err
	}

	for _, header := range queryHeaders {
		request.Header.Add(string(header[0]), string(header[1]))
	}

	client := s.clientsPool.Get().(*http.Client)
	defer s.clientsPool.Put(client)

	response, err := client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = response.Body.Close() }()

	// Read response body using a pooled reader to reduce allocations.
	var bodyBuf bytes.Buffer
	_, err = io.Copy(&bodyBuf, response.Body)
	if err != nil {
		return 0, nil, nil, err
	}

	return response.StatusCode, response.Header, bodyBuf.Bytes(), nil
}
