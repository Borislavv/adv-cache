package middleware

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/server/config"
	"github.com/valyala/fasthttp"
)

type WatermarkMiddleware struct {
	ctx    context.Context
	config fasthttpconfig.Configurator
}

func NewWatermarkMiddleware(ctx context.Context, config fasthttpconfig.Configurator) *WatermarkMiddleware {
	return &WatermarkMiddleware{ctx: ctx, config: config}
}

func (m *WatermarkMiddleware) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Add("X-Server-Name", m.config.GetHttpServerName())

		next(ctx)
	}
}
