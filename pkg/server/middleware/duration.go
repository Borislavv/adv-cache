package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/server/config"
	"github.com/valyala/fasthttp"
	"strconv"
	"time"
)

type Duration struct {
	ctx    context.Context
	config fasthttpconfig.Configurator
}

func NewDuration(ctx context.Context, config fasthttpconfig.Configurator) *Duration {
	return &Duration{ctx: ctx, config: config}
}

func (m *Duration) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		from := time.Now()

		next(ctx)

		ctx.Response.Header.Add("Server-Timing", "p;dur="+strconv.Itoa(int(time.Since(from).Milliseconds())))
	}
}
