package httpserver

import (
	"context"
	"errors"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/http/server/controller"
	"github.com/Borislavv/advanced-cache/pkg/http/server/middleware"
	"github.com/fasthttp/router"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"strings"
	"sync"
	"time"
)

type HTTP struct {
	ctx    context.Context
	config config.Config
	server *fasthttp.Server
}

func New(
	ctx context.Context,
	config config.Config,
	controllers []controller.HttpController,
	middlewares []middleware.HttpMiddleware,
) (*HTTP, error) {
	s := &HTTP{ctx: ctx, config: config}
	s.initServer(s.buildRouter(controllers), middlewares)
	return s, nil
}

func (s *HTTP) ListenAndServe() {
	wg := &sync.WaitGroup{}
	defer wg.Wait()

	wg.Add(1)
	go s.serve(wg)

	wg.Add(1)
	go s.shutdown(wg)
}

func (s *HTTP) serve(wg *sync.WaitGroup) {
	defer wg.Done()

	apiCfg := s.config.Api()
	name := apiCfg.Name
	port := apiCfg.Port
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}

	log.Info().Msgf("[server] %v was started on %v", name, port)
	defer log.Info().Msgf("[server] %v was stopped on %v", name, port)

	if err := s.server.ListenAndServe(port); err != nil {
		log.Error().Err(err).Msgf("[server] %v failed to listen and serve port %v: %v", name, port, err.Error())
	}
}

func (s *HTTP) shutdown(wg *sync.WaitGroup) {
	defer wg.Done()

	<-s.ctx.Done()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	if err := s.server.ShutdownWithContext(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Warn().Msgf("[server] %v shutdown failed: %v", s.config.Api().Name, err.Error())
		}
		return
	}
}

func (s *HTTP) buildRouter(controllers []controller.HttpController) *router.Router {
	r := router.New()
	// set up other controllers
	for _, contr := range controllers {
		contr.AddRoute(r)
	}
	return r
}

func (s *HTTP) wrapMiddlewaresOverRouterHandler(
	handler fasthttp.RequestHandler,
	middlewares []middleware.HttpMiddleware,
) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		s.mergeMiddlewares(handler, middlewares)(ctx)
	}
}

func (s *HTTP) mergeMiddlewares(
	handler fasthttp.RequestHandler,
	middlewares []middleware.HttpMiddleware,
) fasthttp.RequestHandler {
	// last middlewares must be applied at the end
	// in this case we must start the cycle from the end of slice
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i].Middleware(handler)
	}
	return handler
}

func (s *HTTP) initServer(r *router.Router, middlewares []middleware.HttpMiddleware) {
	s.server = &fasthttp.Server{
		Handler:                       s.wrapMiddlewaresOverRouterHandler(r.Handler, middlewares),
		GetOnly:                       true,                   // Only allow HTTP GET requests, rejecting other methods for simplicity and security.
		ReduceMemoryUsage:             true,                   // Reuse internal buffers aggressively to lower memory footprint and GC overhead.
		DisablePreParseMultipartForm:  true,                   // Disable built-in multipart form parsing to avoid unnecessary allocations when not needed.
		DisableHeaderNamesNormalizing: true,                   // Prevent normalization of header names to save CPU cycles when handling high request rates.
		CloseOnShutdown:               true,                   // Ensure that all open connections are closed when the server shuts down gracefully.
		Concurrency:                   4_000_000,              // Configure the maximum number of concurrently handled requests to support extreme load.
		ReadBufferSize:                4 * 1024,               // Allocate a 32 KiB read buffer, aligned with typical CPU cache lines for optimal throughput.
		WriteBufferSize:               4 * 1024,               // Allocate a 32 KiB read buffer, aligned with typical CPU cache lines for optimal throughput.
		ReadTimeout:                   500 * time.Millisecond, // Maximum time allowed to read the full request (headers + body) to mitigate slowloris attacks.
		WriteTimeout:                  500 * time.Millisecond, // Maximum time allowed to write the response to the client to prevent stalled connections.
		IdleTimeout:                   60 * time.Second,       // Maximum idle time before a keep-alive connection is closed, balancing reuse and resource release.
		TCPKeepalive:                  true,                   // Enable OS-level TCP keep-alive probes to detect and clean up dead peer connections.
		TCPKeepalivePeriod:            30 * time.Second,       // Interval between TCP keep-alive probes, ensuring timely detection of dead peers.
		NoDefaultServerHeader:         true,                   // Suppress the default “Server” response header to reduce server fingerprinting.
		MaxRequestBodySize:            10 << 20,               // 10 MiB capacity - the maximum request body size to 10 MiB to guard against excessively large payloads.
	}
}
