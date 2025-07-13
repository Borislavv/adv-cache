package advancedcache

import (
	"bytes"
	"net/http"
	"sync"
)

var capturersPool = sync.Pool{
	New: func() interface{} {
		return &captureRW{
			header:      make(http.Header),
			body:        new(bytes.Buffer),
			status:      0,
			wroteHeader: false,
		}
	},
}

type captureRW struct {
	header      http.Header
	body        *bytes.Buffer
	status      int
	wroteHeader bool
}

func newCaptureRW() (capturer *captureRW, releaseFn func()) {
	capturer = capturersPool.Get().(*captureRW)
	return capturer, func() {
		capturersPool.Put(capturer.reset())
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

func (c *captureRW) reset() *captureRW {
	c.body.Reset()
	for key, _ := range c.header {
		delete(c.header, key)
	}
	return c
}
