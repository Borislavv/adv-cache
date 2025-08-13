package middleware

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/valyala/fasthttp"
)

type FingerprintMiddleware struct {
	serverName []byte
}

func NewFingerprintMiddleware(cfg config.Config) FingerprintMiddleware {
	return FingerprintMiddleware{serverName: []byte(cfg.Api().Name)}
}

func (f FingerprintMiddleware) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.SetServerBytes(f.serverName)
		next(ctx)
	}
}
