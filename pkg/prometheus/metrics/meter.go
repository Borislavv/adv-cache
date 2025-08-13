package metrics

import (
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics/keyword"
	"github.com/VictoriaMetrics/metrics"
)

// Meter defines methods for recording application metrics.
type Meter interface {
	SetHits(value uint64)
	SetMisses(value uint64)
	SetErrors(value uint64)
	SetPanics(value uint64)
	SetProxiedNum(value uint64)
	SetRPS(value float64)
	SetCacheLength(count uint64)
	SetCacheMemory(bytes uint64)
}

// Metrics implements Meter using VictoriaMetrics metrics.
type Metrics struct{}

// New creates a new Metrics instance.
func New() *Metrics {
	return &Metrics{}
}

func (m *Metrics) SetHits(value uint64) {
	metrics.GetOrCreateCounter(keyword.Hits).Set(value)
}

func (m *Metrics) SetMisses(value uint64) {
	metrics.GetOrCreateCounter(keyword.Misses).Set(value)
}

func (m *Metrics) SetRPS(value float64) {
	metrics.GetOrCreateGauge(keyword.RPS, nil).Set(value)
}

func (m *Metrics) SetCacheMemory(bytes uint64) {
	metrics.GetOrCreateCounter(keyword.MapMemoryUsageMetricName).Set(bytes)
}

func (m *Metrics) SetErrors(value uint64) {
	metrics.GetOrCreateCounter(keyword.Errored).Set(value)
}

func (m *Metrics) SetPanics(value uint64) {
	metrics.GetOrCreateCounter(keyword.Panicked).Set(value)
}

func (m *Metrics) SetProxiedNum(value uint64) {
	metrics.GetOrCreateCounter(keyword.Proxied).Set(value)
}

func (m *Metrics) SetCacheLength(count uint64) {
	metrics.GetOrCreateCounter(keyword.MapLength).Set(count)
}
