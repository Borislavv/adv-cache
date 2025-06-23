package middleware

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/server/config"
	"github.com/valyala/fasthttp"
)

var watermarkHeaderKey = []byte("X-Fasthttp-Watermark")

type WatermarkMiddleware struct {
	ctx        context.Context
	serverName []byte
}

func NewWatermarkMiddleware(ctx context.Context, config fasthttpconfig.Configurator) *WatermarkMiddleware {
	return &WatermarkMiddleware{ctx: ctx, serverName: []byte(config.GetHttpServerName())}
}

func (m *WatermarkMiddleware) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.SetCanonical(watermarkHeaderKey, m.serverName)
		next(ctx)
	}
}
