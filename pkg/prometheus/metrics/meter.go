package metrics

import (
	"bytes"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics/keyword"
	"github.com/VictoriaMetrics/metrics"
	"strconv"
	"sync"
	"time"
	"unsafe"
)

var (
	bufPool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
	timersPool = sync.Pool{
		New: func() interface{} {
			return new(Timer)
		},
	}
	pathBytes   = []byte(`{path="`)
	methodBytes = []byte(`",method="`)
	statusBytes = []byte(`",status="`)
	closerBytes = []byte(`"}`)
)

// Meter defines methods for recording application metrics.
type Meter interface {
	IncTotal(path, method, status []byte)
	IncStatus(path, method, status []byte)
	NewResponseTimeTimer(path, method []byte) *Timer
	FlushResponseTimeTimer(t *Timer)
	SetCacheLength(count int64)
	SetCacheMemory(bytes int64)
}

// Metrics implements Meter using VictoriaMetrics metrics.
type Metrics struct{}

// New creates a new Metrics instance.
func New() *Metrics {
	return &Metrics{}
}

// Precompute status code strings for performance.
var statuses [599]string

func init() {
	for i := 100; i < len(statuses); i++ {
		statuses[i] = strconv.Itoa(i)
	}
}

// IncTotal increments total requests or responses depending on status.
func (m *Metrics) IncTotal(path, method, status []byte) {
	name := keyword.TotalHttpRequestsMetricName
	if len(status) > 0 {
		name = keyword.TotalHttpResponsesMetricName
	}
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	defer buf.Reset()

	buf.Write(name)
	buf.Write(pathBytes)
	buf.Write(path)
	buf.Write(methodBytes)
	buf.Write(method)
	if len(status) > 0 {
		buf.Write(statusBytes)
		buf.Write(status)
	}
	buf.Write(closerBytes)

	bufBytes := buf.Bytes()
	bufStr := *(*string)(unsafe.Pointer(&bufBytes))

	metrics.GetOrCreateCounter(bufStr).Inc()
}

// IncStatus increments a counter for HTTP response statuses.
func (m *Metrics) IncStatus(path, method, status []byte) {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	defer buf.Reset()

	buf.Write(keyword.HttpResponseStatusesMetricName)
	buf.Write(pathBytes)
	buf.Write(path)
	buf.Write(methodBytes)
	buf.Write(method)
	buf.Write(statusBytes)
	buf.Write(status)
	buf.Write(closerBytes)

	metrics.GetOrCreateCounter(buf.String()).Inc()
}

// SetCacheMemory updates the gauge for total cache memory usage in bytes.
func (m *Metrics) SetCacheMemory(bytes int64) {
	metrics.GetOrCreateCounter(keyword.MapMemoryUsageMetricName).Set(uint64(bytes))
}

// SetCacheLength updates the gauge for total number of items in the cache.
func (m *Metrics) SetCacheLength(count int64) {
	metrics.GetOrCreateCounter(keyword.MapLength).Set(uint64(count))
}

// Timer tracks start of an operation for timing metrics.
type Timer struct {
	name  string
	start int64
}

// NewResponseTimeTimer creates a Timer for measuring response time of given path and method.
func (m *Metrics) NewResponseTimeTimer(path, method []byte) *Timer {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	defer buf.Reset()

	buf.Write(keyword.HttpResponseTimeMsMetricName)
	buf.Write(pathBytes)
	buf.Write(path)
	buf.Write(methodBytes)
	buf.Write(method)
	buf.Write(closerBytes)

	t := timersPool.Get().(*Timer)
	t.name = buf.String()
	t.start = time.Now().UnixNano()

	return t
}

// FlushResponseTimeTimer records the elapsed time since Timer creation into a histogram.
func (m *Metrics) FlushResponseTimeTimer(t *Timer) {
	delta := float64(time.Now().UnixNano()-t.start) / 1e9
	metrics.GetOrCreateHistogram(t.name).Update(delta)
	timersPool.Put(t)
}
