package middleware

import (
	"bytes"
	"net/http"
)

type captureResponseWriter struct {
	wrapped     http.ResponseWriter
	body        *bytes.Buffer
	statusCode  int
	headers     http.Header
	wroteHeader bool
}

func newCaptureResponseWriter(w http.ResponseWriter) *captureResponseWriter {
	return &captureResponseWriter{
		wrapped:    w,
		body:       new(bytes.Buffer),
		statusCode: http.StatusOK,
		headers:    make(http.Header),
	}
}

func (w *captureResponseWriter) Header() http.Header {
	// intercept and work with our copy of headers
	return w.headers
}

func (w *captureResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return // prevent double WriteHeader
	}
	w.statusCode = code
	w.wroteHeader = true

	// copy headers to the underlying writer before writing header
	for k, v := range w.headers {
		for _, vv := range v {
			w.wrapped.Header().Add(k, vv)
		}
	}
	w.wrapped.WriteHeader(code)
}

func (w *captureResponseWriter) Write(b []byte) (int, error) {
	// ensure WriteHeader is called if not already done
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	w.body.Write(b) // Save to buffer
	return w.wrapped.Write(b)
}
