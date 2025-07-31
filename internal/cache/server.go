package cache

import (
	"context"
	"errors"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/router"
	httpserver "github.com/Borislavv/advanced-cache/pkg/server"
	"github.com/Borislavv/advanced-cache/pkg/server/middleware"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"sync"
	"sync/atomic"
)

var (
	InitFailedErrorMessage = "[server] init. failed"
)

// Http interface exposes methods for starting and liveness probing.
type Http interface {
	Start()
	IsAlive() bool
}

// HttpServer implements Http, wraps all dependencies required for running the HTTP server.
type HttpServer struct {
	ctx context.Context
	cfg *config.Cache

	server        httpserver.Server
	isServerAlive *atomic.Bool

	cache fasthttp.RequestHandler
}

// New creates a new HttpServer, initializing metrics and the HTTP server itself.
// If any step fails, returns an error and performs cleanup.
func New(ctx context.Context, cfg *config.Cache, upstream router.Upstream, cache fasthttp.RequestHandler, routes ...router.Route) (*HttpServer, error) {
	var err error

	srv := &HttpServer{
		ctx:           ctx,
		cfg:           cfg,
		cache:         cache,
		isServerAlive: &atomic.Bool{},
	}

	// Initialize HTTP server with all controllers and middlewares.
	if err = srv.initServer(router.NewRouter(upstream, routes...)); err != nil {
		log.Err(err).Msg(InitFailedErrorMessage)
		return nil, errors.New(InitFailedErrorMessage)
	}

	return srv, nil
}

// Start runs the HTTP server in a goroutine and waits for it to finish.
func (s *HttpServer) Start() {
	waitCh := make(chan struct{})

	go func() {
		defer close(waitCh)
		wg := &sync.WaitGroup{}
		defer wg.Wait()
		s.spawnServer(wg)
	}()

	<-waitCh
}

// IsAlive returns true if the server is marked as alive.
func (s *HttpServer) IsAlive() bool {
	return s.isServerAlive.Load()
}

// spawnServer starts the HTTP server in a new goroutine, sets server liveness flags, and blocks until it exits.
func (s *HttpServer) spawnServer(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer func() {
			s.isServerAlive.Store(false)
			wg.Done()
		}()
		s.isServerAlive.Store(true)
		s.server.ListenAndServe()
	}()
}

// initServer creates the HTTP server instance, sets up controllers and middlewares, and stores the result.
func (s *HttpServer) initServer(router *router.Router) error {
	// Compose server with controllers and middlewares.
	if server, err := httpserver.New(s.ctx, s.cfg, router, s.cache, s.middlewares()); err != nil {
		log.Err(err).Msg(InitFailedErrorMessage)
		return errors.New(InitFailedErrorMessage)
	} else {
		s.server = server
	}

	return nil
}

// middlewares returns the request middlewares for the server, executed in reverse order.
func (s *HttpServer) middlewares() []middleware.HttpMiddleware {
	return []middleware.HttpMiddleware{
		/** exec 1st. */ middleware.NewApplicationJsonMiddleware(), // Sets Content-Type
	}
}
