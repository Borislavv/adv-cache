package middleware

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/prometheus/metrics"
	"github.com/valyala/fasthttp"
	"strconv"
	"unsafe"
)

type PrometheusMetrics struct {
	ctx     context.Context
	metrics metrics.Meter
}

func NewPrometheusMetrics(ctx context.Context, metrics metrics.Meter) *PrometheusMetrics {
	return &PrometheusMetrics{ctx: ctx, metrics: metrics}
}

func (m *PrometheusMetrics) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		pth := ctx.Path()
		path := *(*string)(unsafe.Pointer(&pth))

		mthd := ctx.Method()
		method := *(*string)(unsafe.Pointer(&mthd))

		timer := m.metrics.NewResponseTimeTimer(path, method)

		m.metrics.IncTotal(path, method, "")

		next(ctx)

		m.metrics.IncStatus(path, method, strconv.Itoa(ctx.Response.StatusCode()))
		m.metrics.IncTotal(path, method, strconv.Itoa(ctx.Response.StatusCode()))

		m.metrics.FlushResponseTimeTimer(timer)
	}
}
