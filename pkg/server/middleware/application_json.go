package middleware

import "github.com/valyala/fasthttp"

type ApplicationJsonMiddleware struct{}

func NewApplicationJsonMiddleware() ApplicationJsonMiddleware {
	return ApplicationJsonMiddleware{}
}

func (ApplicationJsonMiddleware) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.SetContentType("application/json")

		next(ctx)
	}
}
