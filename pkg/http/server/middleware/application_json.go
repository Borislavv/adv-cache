package middleware

import "github.com/valyala/fasthttp"

var contentTypeValue = []byte("application/json")

type ApplicationJsonMiddleware struct{}

func NewApplicationJsonMiddleware() ApplicationJsonMiddleware {
	return ApplicationJsonMiddleware{}
}

func (ApplicationJsonMiddleware) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.SetContentTypeBytes(contentTypeValue)
		next(ctx)
	}
}
