package route

import (
	"github.com/VictoriaMetrics/metrics"
	"github.com/valyala/fasthttp"
)

const metricsRoutePath = "/metrics"

type MetricsRoute struct{}

func NewMetricsRoute() *MetricsRoute {
	return &MetricsRoute{}
}

func (c *MetricsRoute) Handle(r *fasthttp.RequestCtx) error {
	r.SetStatusCode(fasthttp.StatusOK)
	metrics.WritePrometheus(r, true)
	return nil
}

func (c *MetricsRoute) Paths() []string {
	return []string{metricsRoutePath}
}

func (c *MetricsRoute) IsEnabled() bool {
	return IsCacheEnabled()
}

func (c *MetricsRoute) IsInternal() bool {
	return true
}
