package advancedcache

import (
	"bytes"
	"net/http"
)

type captureRW struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func newCaptureRW() *captureRW {
	return &captureRW{
		header: make(http.Header),
		status: http.StatusOK,
	}
}

func (c *captureRW) Header() http.Header {
	return c.header
}

func (c *captureRW) WriteHeader(code int) {
	if !c.wroteHeader {
		c.status = code
		c.wroteHeader = true
	}
}

func (c *captureRW) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	return c.body.Write(b)
}
