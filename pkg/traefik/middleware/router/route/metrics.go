package route

import (
	"github.com/VictoriaMetrics/metrics"
	"net/http"
)

const metricsRoutePath = "/metrics"

type MetricsRoute struct{}

func NewMetricsRoute() *MetricsRoute {
	return &MetricsRoute{}
}

func (c *MetricsRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	metrics.WritePrometheus(w, true)
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
