package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/traefik/middleware/router"
	"net/http"
)

type TraefikCacheMiddleware struct {
	ctx    context.Context
	next   http.Handler
	name   string
	cfg    *config.Cache
	router *router.Router
}

func New(ctx context.Context, next http.Handler, cfg *config.TraefikIntermediateConfig, name string) http.Handler {
	cacheMiddleware := &TraefikCacheMiddleware{
		ctx:  ctx,
		next: next,
		name: name,
	}

	if err := cacheMiddleware.run(ctx, cfg); err != nil {
		panic(err)
	}

	return cacheMiddleware
}

func (m *TraefikCacheMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.router.ServeHTTP(w, r)
}
