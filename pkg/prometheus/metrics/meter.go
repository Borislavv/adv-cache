package metrics

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Borislavv/traefik-http-cache-plugin/pkg/prometheus/metrics/keyword"
	"github.com/VictoriaMetrics/metrics"
)

type Meter interface {
	IncTotal(path string, method string, status string)
	IncStatus(path string, method string, status string)
	NewResponseTimeTimer(path string, method string) *Timer
	FlushResponseTimeTimer(t *Timer)
}

type Metrics struct{}

func New() (*Metrics, error) {
	return &Metrics{}, nil
}

var statuses [600]string

func init() {
	for i := 100; i <= 599; i++ {
		statuses[i] = strconv.Itoa(i)
	}
}

func (m *Metrics) IncTotal(path, method, status string) {
	safePath, safeMethod := sanitize(path), sanitize(method)

	if status != "" {
		statusCode, err := strconv.Atoi(status)
		if err != nil || statusCode < 100 || statusCode >= len(statuses) {
			panic("invalid status code: " + status)
		}
		safeStatus := statuses[statusCode]

		buf := getBuf()
		defer putBuf(buf)

		*buf = append(*buf, keyword.TotalHttpResponsesMetricName...)
		*buf = append(*buf, `{path="`...)
		*buf = append(*buf, safePath...)
		*buf = append(*buf, `",method="`...)
		*buf = append(*buf, safeMethod...)
		*buf = append(*buf, `",status="`...)
		*buf = append(*buf, safeStatus...)
		*buf = append(*buf, `"}`...)

		metrics.GetOrCreateCounter(string(*buf)).Inc()
		return
	}

	buf := getBuf()
	defer putBuf(buf)

	*buf = append(*buf, keyword.TotalHttpRequestsMetricName...)
	*buf = append(*buf, `{path="`...)
	*buf = append(*buf, safePath...)
	*buf = append(*buf, `",method="`...)
	*buf = append(*buf, safeMethod...)
	*buf = append(*buf, `"}`...)

	metrics.GetOrCreateCounter(string(*buf)).Inc()
}

func (m *Metrics) IncStatus(path, method, status string) {
	statusCode, err := strconv.Atoi(status)
	if err != nil || statusCode < 100 || statusCode >= len(statuses) {
		panic("invalid status code: " + status)
	}
	safePath := sanitize(path)
	safeMethod := sanitize(method)
	safeStatus := statuses[statusCode]

	buf := getBuf()
	defer putBuf(buf)

	*buf = append(*buf, keyword.HttpResponseStatusesMetricName...)
	*buf = append(*buf, `{path="`...)
	*buf = append(*buf, safePath...)
	*buf = append(*buf, `",method="`...)
	*buf = append(*buf, safeMethod...)
	*buf = append(*buf, `",status="`...)
	*buf = append(*buf, safeStatus...)
	*buf = append(*buf, `"}`...)

	metrics.GetOrCreateCounter(string(*buf)).Inc()
}

// Timer — пул-ориентированный трекер времени
type Timer struct {
	start time.Time
	buf   *bytes.Buffer
}

var timerPool = sync.Pool{
	New: func() any {
		return &Timer{
			buf: bytes.NewBuffer(make([]byte, 0, 128)),
		}
	},
}

func (m *Metrics) NewResponseTimeTimer(path, method string) *Timer {
	safePath, safeMethod := sanitize(path), sanitize(method)

	t := timerPool.Get().(*Timer)
	t.start = time.Now()
	t.buf.Reset()

	t.buf.WriteString(keyword.HttpResponseTimeMsMetricName)
	t.buf.WriteString(`{path="`)
	t.buf.WriteString(safePath)
	t.buf.WriteString(`",method="`)
	t.buf.WriteString(safeMethod)
	t.buf.WriteString(`"}`)

	return t
}

func (m *Metrics) FlushResponseTimeTimer(t *Timer) {
	durationMs := float64(time.Since(t.start).Milliseconds())
	metrics.GetOrCreateHistogram(t.buf.String()).Update(durationMs)
	timerPool.Put(t)
}

// sanitize делает экранирование кавычек и слэшей в метках
func sanitize(s string) string {
	if !strings.ContainsAny(s, `"\`) {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// ===== buf []byte pooling =====

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 256)
		return &b
	},
}

func getBuf() *[]byte {
	return bufPool.Get().(*[]byte)
}

func putBuf(b *[]byte) {
	*b = (*b)[:0]
	bufPool.Put(b)
}
