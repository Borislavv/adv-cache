package middleware

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/valyala/fasthttp"
)

var originServerBytes = []byte("X-Origin-Server")

type ServerNameMiddleware struct {
	serverName []byte
}

func NewServerNameMiddleware(cfg config.Config) ServerNameMiddleware {
	return ServerNameMiddleware{serverName: []byte(cfg.Api().Name)}
}

func (f ServerNameMiddleware) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		next(ctx)
		if len(ctx.Response.Header.Server()) > 0 {
			ctx.Response.Header.SetBytesKV(originServerBytes, ctx.Response.Header.Server())
		}
		ctx.Response.Header.SetServerBytes(f.serverName)
	}
}
