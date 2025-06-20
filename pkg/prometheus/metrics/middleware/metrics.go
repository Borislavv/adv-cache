package middleware

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/prometheus/metrics"
	"github.com/valyala/fasthttp"
	"runtime"
	"strconv"
	"unsafe"
)

var emptyStr = ""

type PrometheusMetrics struct {
	ctx   context.Context
	meter metrics.Meter
	codes [599]string
}

func NewPrometheusMetrics(ctx context.Context, meter metrics.Meter) *PrometheusMetrics {
	codes := [599]string{}
	for code := 0; code < 599; code++ {
		codes[code] = strconv.Itoa(code)
	}
	return &PrometheusMetrics{
		ctx:   ctx,
		meter: meter,
		codes: codes,
	}
}

func (m *PrometheusMetrics) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		pth := ctx.Path()
		method := ctx.Method()

		pathStr := *(*string)(unsafe.Pointer(&pth))
		methodStr := *(*string)(unsafe.Pointer(&method))

		timer := m.meter.NewResponseTimeTimer(pathStr, methodStr)
		m.meter.IncTotal(pathStr, methodStr, emptyStr) // total requests (no status)

		next(ctx)

		status := ctx.Response.StatusCode()
		m.meter.IncStatus(pathStr, methodStr, m.codes[status])
		m.meter.IncTotal(pathStr, methodStr, m.codes[status])
		m.meter.FlushResponseTimeTimer(timer)

		runtime.Gosched()
	}
}
