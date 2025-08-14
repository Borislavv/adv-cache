package middleware

import (
	"github.com/valyala/fasthttp"
)

var (
	hostK = []byte("Host")
	hostV = []byte("0.0.0.0")
)
var applicationJsonBytes = []byte("application/json")

type ApplicationJsonMiddleware struct{}

func NewApplicationJsonMiddleware() ApplicationJsonMiddleware {
	return ApplicationJsonMiddleware{}
}

func (ApplicationJsonMiddleware) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		next(ctx)
		if len(ctx.Response.Header.ContentType()) == 0 {
			ctx.Response.Header.SetContentTypeBytes(applicationJsonBytes)
		}
		ctx.Response.Header.SetBytesKV(hostK, hostV)
	}
}
