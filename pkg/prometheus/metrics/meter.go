package metrics

import (
	"bytes"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics/keyword"
	"github.com/VictoriaMetrics/metrics"
	"sync"
)

var (
	bufPool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
	pathBytes   = []byte(`{path="`)
	methodBytes = []byte(`",method="`)
	statusBytes = []byte(`",status="`)
	closerBytes = []byte(`"}`)
)

// Meter defines methods for recording application metrics.
type Meter interface {
	SetHits(value uint64)
	SetMisses(value uint64)
	SetErrors(value uint64)
	SetProxiedNum(value uint64)
	SetRPS(value float64)
	SetCacheLength(count uint64)
	SetCacheMemory(bytes uint64)
	SetAvgResponseTime(avg float64)
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
	metrics.GetOrCreateCounter(keyword.UpstreamErrors).Set(value)
}

func (m *Metrics) SetProxiedNum(value uint64) {
	metrics.GetOrCreateCounter(keyword.NumOfProxiedRequests).Set(value)
}

func (m *Metrics) SetCacheLength(count uint64) {
	metrics.GetOrCreateCounter(keyword.MapLength).Set(count)
}

func (m *Metrics) SetAvgResponseTime(avgDuration float64) {
	metrics.GetOrCreateGauge(keyword.AvgDuration, nil).Set(avgDuration)
}
