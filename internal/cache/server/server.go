package server

import (
	"context"
	"errors"
	"github.com/Borislavv/traefik-http-cache-plugin/internal/cache/api"
	"github.com/Borislavv/traefik-http-cache-plugin/internal/cache/config"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/k8s/probe/liveness"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/prometheus/metrics"
	api2 "github.com/Borislavv/traefik-http-cache-plugin/pkg/prometheus/metrics/controller"
	middleware2 "github.com/Borislavv/traefik-http-cache-plugin/pkg/prometheus/metrics/middleware"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/repository"
	httpserver "github.com/Borislavv/traefik-http-cache-plugin/pkg/server"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/server/controller"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/server/middleware"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage"
	"github.com/rs/zerolog/log"
	"sync"
	"sync/atomic"
)

// Error messages used for server and metrics initialization.
var (
	InitFailedErrorMessage        = "[server] init. failed"
	MetricsInitFailedErrorMessage = "[server] init. prometheus metrics failed"
)

// Http interface exposes methods for starting and liveness probing.
type Http interface {
	Start()
	IsAlive() bool
}

// HttpServer implements Http, wraps all dependencies required for running the HTTP server.
type HttpServer struct {
	ctx    context.Context
	cancel context.CancelFunc

	cfg           *config.Config
	db            storage.Storage
	backend       repository.Backender
	probe         liveness.Prober
	metrics       metrics.Meter
	server        httpserver.Server
	isServerAlive *atomic.Bool
}

// New creates a new HttpServer, initializing metrics and the HTTP server itself.
// If any step fails, returns an error and performs cleanup.
func New(
	ctx context.Context,
	cfg *config.Config,
	db storage.Storage,
	backend repository.Backender,
	probe liveness.Prober,
) (*HttpServer, error) {
	var err error

	// Create cancellable context for graceful shutdown.
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	srv := &HttpServer{
		ctx:           ctx,
		cancel:        cancel,
		cfg:           cfg,
		db:            db,
		backend:       backend,
		probe:         probe,
		isServerAlive: &atomic.Bool{},
	}

	// Initialize Metrics or other metrics.
	if err = srv.initMetrics(); err != nil {
		log.Err(err).Msg(MetricsInitFailedErrorMessage)
		return nil, errors.New(MetricsInitFailedErrorMessage)
	}

	// Initialize HTTP server with all controllers and middlewares.
	if err = srv.initServer(); err != nil {
		log.Err(err).Msg(InitFailedErrorMessage)
		return nil, errors.New(InitFailedErrorMessage)
	}

	return srv, nil
}

// Start runs the HTTP server in a goroutine and waits for it to finish.
func (s *HttpServer) Start() {
	defer s.stop()

	waitCh := make(chan struct{})

	go func() {
		defer close(waitCh)
		wg := &sync.WaitGroup{}
		defer wg.Wait()
		s.spawnServer(wg)
	}()

	<-waitCh
}

// stop cancels the context, signaling shutdown to all server goroutines.
func (s *HttpServer) stop() {
	s.cancel()
	s.db.Stop()
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

// initCtx allows updating/replacing the server's context and cancel func.
func (s *HttpServer) initCtx(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.ctx = ctx
	s.cancel = cancel
}

// initMetrics initializes Metrics (or custom) metrics registry and binds it to the server.
func (s *HttpServer) initMetrics() error {
	prometheusMetrics, err := metrics.New()
	if err != nil {
		log.Err(err).Msg(MetricsInitFailedErrorMessage)
		return errors.New(MetricsInitFailedErrorMessage)
	}
	s.metrics = prometheusMetrics
	return nil
}

// initServer creates the HTTP server instance, sets up controllers and middlewares, and stores the result.
func (s *HttpServer) initServer() error {
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel

	// Compose server with controllers and middlewares.
	if server, err := httpserver.New(ctx, s.cfg, s.controllers(), s.middlewares()); err != nil {
		cancel()
		log.Err(err).Msg(InitFailedErrorMessage)
		return errors.New(InitFailedErrorMessage)
	} else {
		s.server = server
	}

	return nil
}

// controllers returns all HTTP controllers for the server (endpoints/handlers).
func (s *HttpServer) controllers() []controller.HttpController {
	return []controller.HttpController{
		api.NewCacheController(s.ctx, s.cfg, s.db, s.backend), // Main cache handler
		liveness.NewController(s.probe),                       // Liveness/healthcheck endpoint
		api2.NewPrometheusMetrics(s.ctx),                      // Metrics metrics endpoint
	}
}

// middlewares returns the request middlewares for the server, executed in reverse order.
func (s *HttpServer) middlewares() []middleware.HttpMiddleware {
	return []middleware.HttpMiddleware{
		/** exec 1st. */ middleware.NewApplicationJsonMiddleware(), // Sets Content-Type
		/** exec 2nd. */ middleware.NewWatermarkMiddleware(s.ctx, s.cfg), // Adds watermark info
		/** exec 3rd. */ middleware2.NewPrometheusMetrics(s.ctx, s.metrics), // Metrics instrumentation
	}
}
