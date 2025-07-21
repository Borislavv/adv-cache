package httpwriter

import (
	"bytes"
	"github.com/Borislavv/advanced-cache/pkg/pools"
	"net/http"
	"sync"
	"unsafe"
)

// CaptureResponseWriter captures status code, headers, and body.
type CaptureResponseWriter struct {
	wrapped     http.ResponseWriter
	headers     http.Header   // our copy before writing
	body        *bytes.Buffer // captured response body
	statusCode  int
	wroteHeader bool
	handled     bool
}

// Global sync.Pool to reduce allocs.
var captureRWPool = sync.Pool{
	New: func() interface{} {
		return &CaptureResponseWriter{
			headers: make(http.Header),
			body:    new(bytes.Buffer),
		}
	},
}

// NewCaptureResponseWriter gets one from Pool.
func NewCaptureResponseWriter(w http.ResponseWriter) (*CaptureResponseWriter, func()) {
	crw := captureRWPool.Get().(*CaptureResponseWriter)
	crw.wrapped = w
	crw.statusCode = http.StatusOK
	crw.wroteHeader = false
	return crw, func() {
		captureRWPool.Put(crw.Reset())
	}
}

func (crw *CaptureResponseWriter) Header() http.Header {
	return crw.headers
}

func (crw *CaptureResponseWriter) WriteHeader(status int) {
	if crw.wroteHeader {
		return
	}
	crw.statusCode = status
	crw.wroteHeader = true

	// Copy captured headers to underlying ResponseWriter
	dst := crw.wrapped.Header()
	for k, v := range crw.headers {
		for _, vv := range v {
			dst.Add(k, vv)
		}
	}
	crw.wrapped.WriteHeader(status)
}

func (crw *CaptureResponseWriter) Write(b []byte) (int, error) {
	if !crw.wroteHeader {
		crw.WriteHeader(http.StatusOK)
	}
	crw.body.Write(b)
	return crw.wrapped.Write(b)
}

func (crw *CaptureResponseWriter) Reset() *CaptureResponseWriter {
	crw.wrapped = nil
	crw.statusCode = http.StatusOK
	crw.wroteHeader = false
	crw.body.Reset()
	for k := range crw.headers {
		delete(crw.headers, k)
	}
	return crw
}

func (crw *CaptureResponseWriter) StatusCode() int {
	return crw.statusCode
}

func (crw *CaptureResponseWriter) SetStatusCode(status int) {
	crw.statusCode = status
}

func (crw *CaptureResponseWriter) Body() []byte {
	return crw.body.Bytes()
}

func (crw *CaptureResponseWriter) SetBody(b []byte) {
	crw.body.Reset()
	crw.body.Write(b)
}

func (crw *CaptureResponseWriter) Headers() http.Header {
	return crw.headers
}

func (crw *CaptureResponseWriter) ExtractPayload() (
	status int, headers *[][2][]byte, body []byte, releaseFn func(*[][2][]byte),
) {
	body = crw.Body()
	status = crw.StatusCode()
	headers, releaseFn = crw.headerToPairs(crw.Headers())
	return status, headers, body, releaseFn
}

var headersToPairsReleaser = func(pairs *[][2][]byte) {
	*pairs = (*pairs)[:0]
	pools.KeyValueSlicePool.Put(pairs)
}

// Helper to convert http.Header to [][2][]byte for storage payload
func (crw *CaptureResponseWriter) headerToPairs(h http.Header) (headers *[][2][]byte, releaserFn func(*[][2][]byte)) {
	headers = pools.KeyValueSlicePool.Get().(*[][2][]byte)
	for k, vv := range h {
		for _, v := range vv {
			*headers = append(*headers, [2][]byte{unsafe.Slice(unsafe.StringData(k), len(k)), unsafe.Slice(unsafe.StringData(v), len(v))})
		}
	}
	return headers, headersToPairsReleaser
}
