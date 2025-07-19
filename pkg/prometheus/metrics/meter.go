package metrics

import (
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics/keyword"
	"github.com/VictoriaMetrics/metrics"
	"strconv"
	"sync"
	"time"
	"unsafe"
)

type Meter interface {
	IncTotal(path, method, status []byte)
	IncStatus(path, method, status []byte)
	NewResponseTimeTimer(path, method []byte) *Timer
	FlushResponseTimeTimer(t *Timer)
	SetCacheLength(count int64)
	SetCacheMemory(bytes int64)
}

type Meterics struct {
	bufPool sync.Pool
	codes   [599][]byte
}

func New() *Meterics {
	codes := [599][]byte{}
	for i := 0; i < 599; i++ {
		codes[i] = []byte(strconv.Itoa(i))
	}
	return &Meterics{
		bufPool: sync.Pool{
			New: func() any {
				b := make([]byte, 0, 128)
				return &b
			},
		},
		codes: codes,
	}
}

func (m *Meterics) getBuf() *[]byte {
	buf := m.bufPool.Get().(*[]byte)
	*buf = (*buf)[:0]
	return buf
}

func (m *Meterics) putBuf(buf *[]byte) {
	m.bufPool.Put(buf)
}

func (m *Meterics) IncTotal(path, method, status []byte) {
	buf := m.getBuf()
	defer m.putBuf(buf)

	name := keyword.TotalHttpRequestsMetricName
	if len(status) > 0 {
		name = keyword.TotalHttpResponsesMetricName
	}
	*buf = append(*buf, name...)
	*buf = append(*buf, `{path="`...)
	*buf = append(*buf, path...)
	*buf = append(*buf, `",method="`...)
	*buf = append(*buf, method...)
	if len(status) > 0 {
		*buf = append(*buf, `",status="`...)
		*buf = append(*buf, status...)
	}
	*buf = append(*buf, `"}`...)
	s := unsafe.String(&(*buf)[0], len(*buf))
	metrics.GetOrCreateCounter(s).Inc()
}

func (m *Meterics) IncStatus(path, method, status []byte) {
	buf := m.getBuf()
	defer m.putBuf(buf)

	*buf = append(*buf, keyword.HttpResponseStatusesMetricName...)
	*buf = append(*buf, `{path="`...)
	*buf = append(*buf, path...)
	*buf = append(*buf, `",method="`...)
	*buf = append(*buf, method...)
	*buf = append(*buf, `",status="`...)
	*buf = append(*buf, status...)
	*buf = append(*buf, `"}`...)
	s := unsafe.String(&(*buf)[0], len(*buf))
	metrics.GetOrCreateCounter(s).Inc()
}

func (m *Meterics) SetCacheMemory(bytes int64) {
	metrics.GetOrCreateCounter(keyword.MapMemoryUsageMetricName).Set(uint64(bytes))
}

func (m *Meterics) SetCacheLength(count int64) {
	metrics.GetOrCreateCounter(keyword.MapLength).Set(uint64(count))
}

type Timer struct {
	name  string
	start int64
}

func (m *Meterics) NewResponseTimeTimer(path, method []byte) *Timer {
	buf := m.getBuf()
	defer m.putBuf(buf)

	*buf = append(*buf, keyword.HttpResponseTimeMsMetricName...)
	*buf = append(*buf, `{path="`...)
	*buf = append(*buf, path...)
	*buf = append(*buf, `",method="`...)
	*buf = append(*buf, method...)
	*buf = append(*buf, `"}`...)
	s := unsafe.String(&(*buf)[0], len(*buf))
	return &Timer{name: s, start: time.Now().UnixNano()}
}

func (m *Meterics) FlushResponseTimeTimer(t *Timer) {
	delta := float64(time.Now().UnixNano()-t.start) / 1e9
	metrics.GetOrCreateHistogram(t.name).Update(delta)
}
