package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/server/config"
	"github.com/Borislavv/advanced-cache/pkg/server/keyword"
	"github.com/rs/zerolog/log"
	"github.com/savsgio/gotils/uuid"
	"github.com/valyala/fasthttp"
)

const (
	XRequestIDHeader   = "X-Request-ID"
	XRequestGUIDHeader = "X-Request-GUID"
)

type ForwardIDsMiddleware struct {
	ctx    context.Context
	config fasthttpconfig.Configurator
}

func NewForwardIDsMiddleware(ctx context.Context, config fasthttpconfig.Configurator) *ForwardIDsMiddleware {
	return &ForwardIDsMiddleware{ctx: ctx, config: config}
}

func (m *ForwardIDsMiddleware) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		// extract x-request-id header from request
		id := string(ctx.Request.Header.Peek(XRequestIDHeader))
		if id == "" {
			id = uuid.V4()
		}

		// extract x-request-guid header from request
		guid := string(ctx.Request.Header.Peek(XRequestGUIDHeader))
		if guid == "" {
			guid = uuid.V4()
		}

		// extract request context
		reqCtx, ok := ctx.UserValue(keyword.CtxKey).(context.Context)
		if !ok {
			log.Warn().Msg("[server] context.Context is not exists into the fasthttp.RequestCtx " +
				"(unable to forward x-request-id and x-request-guid)")
			next(ctx)
			return
		}

		// build updated context which includes x-request-id and x-request-guid values
		reqCtx = context.WithValue(reqCtx, keyword.ReqID, id)
		reqCtx = context.WithValue(reqCtx, keyword.ReqGUID, guid)

		// set up the updated context into *fasthttp.RequestCtx
		ctx.SetUserValue(keyword.CtxKey, reqCtx)

		// write x-request-id and x-request-guid headers
		ctx.Response.Header.Add(XRequestIDHeader, id)
		ctx.Response.Header.Add(XRequestGUIDHeader, guid)

		next(ctx)
	}
}
