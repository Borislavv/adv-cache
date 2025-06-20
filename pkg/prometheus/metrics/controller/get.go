package controller

import (
	"github.com/VictoriaMetrics/metrics"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

const PrometheusMetricsPath = "/metrics"

type PrometheusMetrics struct{}

func NewPrometheusMetrics() *PrometheusMetrics {
	return &PrometheusMetrics{}
}

func (m *PrometheusMetrics) Get(ctx *fasthttp.RequestCtx) {
	ctx.SetContentType("text/plain; charset=utf-8")
	ctx.SetStatusCode(fasthttp.StatusOK)
	metrics.WritePrometheus(ctx, true)
}

func (m *PrometheusMetrics) AddRoute(router *router.Router) {
	router.GET(PrometheusMetricsPath, m.Get)
}
