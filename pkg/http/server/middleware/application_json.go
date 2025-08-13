package middleware

import (
	"github.com/valyala/fasthttp"
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
	}
}
