package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/server/config"
	"github.com/Borislavv/advanced-cache/pkg/server/keyword"
	"github.com/valyala/fasthttp"
)

type InitCtxMiddleware struct {
	ctx    context.Context
	config fasthttpconfig.Configurator
}

func NewInitCtxMiddleware(ctx context.Context, config fasthttpconfig.Configurator) *InitCtxMiddleware {
	return &InitCtxMiddleware{ctx: ctx, config: config}
}

func (m *InitCtxMiddleware) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		reqCtx, reqCtxCancel := context.WithTimeout(m.ctx, m.config.GetHttpServerRequestTimeout())
		defer reqCtxCancel()

		ctx.SetUserValue(keyword.CtxKey, reqCtx)
		ctx.SetUserValue(keyword.CtxCancelKey, reqCtxCancel)

		next(ctx)
	}
}
