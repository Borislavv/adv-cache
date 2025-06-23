package httpserver

import (
	"context"
	"errors"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/server/config"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/server/controller"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/server/middleware"
	"github.com/fasthttp/router"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"sync"
)

type HTTP struct {
	ctx    context.Context
	server *fasthttp.Server
	config fasthttpconfig.Configurator
}

func New(
	ctx context.Context,
	config fasthttpconfig.Configurator,
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

	name := s.config.GetHttpServerName()
	port := s.config.GetHttpServerPort()

	log.Info().Msgf("[fasthttp] %v was started (port: %v)", name, port)
	defer log.Info().Msgf("[fasthttp] %v was stopped (port: %v)", name, port)

	if err := s.server.ListenAndServe(port); err != nil {
		log.Err(err).Msgf("[fasthttp] %v failed to listen and serve port %v: %v", name, port, err.Error())
	}
}

func (s *HTTP) shutdown(wg *sync.WaitGroup) {
	defer wg.Done()

	<-s.ctx.Done()

	ctx, cancel := context.WithTimeout(context.Background(), s.config.GetHttpServerShutDownTimeout())
	defer cancel()

	if err := s.server.ShutdownWithContext(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Warn().Msgf("[fasthttp] %v shutdown failed: %v", s.config.GetHttpServerName(), err.Error())
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
	s.server = &fasthttp.Server{Handler: s.wrapMiddlewaresOverRouterHandler(r.Handler, middlewares)}
}
