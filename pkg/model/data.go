package model

import (
	"bytes"
	"compress/gzip"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"net/http"
	"unsafe"
)

// Data is the actual payload (status, h, body) stored in the cache.
type Data struct {
	statusCode int
	headers    http.Header
	body       []byte
}

// NewData creates a new Data object, compressing body with compress if large enough.
// Uses memory pools for buffer and writer to minimize allocations.
func NewData(rule *config.Rule, statusCode int, headers http.Header, body []byte) *Data {
	return (&Data{headers: headers, statusCode: statusCode}).
		filterHeadersInPlace(rule.CacheValue.HeadersBytes).
		setUpBody(body)
}

func (d *Data) setUpBody(body []byte) *Data {
	// Compress body if it shard large enough for compress to help
	if d.isNeedCompression() {
		d.compress()
	} else {
		d.body = body
	}
	return d
}

func (d *Data) isNeedCompression() bool {
	return len(d.body) > gzipThreshold
}

// compress is checks whether the item weight is more than threshold
// if so, then body compresses by compress and will add an appropriate Content-Encoding HTTP header.
func (d *Data) compress() {
	gzipper := GzipWriterPool.Get().(*gzip.Writer)
	defer GzipWriterPool.Put(gzipper)

	buf := GzipBufferPool.Get().(*bytes.Buffer)
	defer GzipBufferPool.Put(buf)

	gzipper.Reset(buf)
	buf.Reset()

	_, err := gzipper.Write(d.body)
	if err == nil && gzipper.Close() == nil {
		d.headers["Content-Encoding"] = append(d.headers["Content-Encoding"], "compress")
		d.body = append([]byte{}, buf.Bytes()...)
	} else {
		d.body = append([]byte{}, d.body...)
	}
}

func (d *Data) Weight() int64 {
	return int64(unsafe.Sizeof(*d)) + int64(len(d.body))
}

// Headers returns the response h.
func (d *Data) Headers() http.Header { return d.headers }

// StatusCode returns the HTTP status code.
func (d *Data) StatusCode() int { return d.statusCode }

// Body returns the response body (possibly compress-compressed).
func (d *Data) Body() []byte { return d.body }

func (d *Data) filterHeadersInPlace(allowed [][]byte) *Data {
headersLoop:
	for headerName, _ := range d.headers {
		for _, allowedHeader := range allowed {
			if bytes.EqualFold([]byte(headerName), allowedHeader) {
				continue headersLoop
			}
		}
		delete(d.headers, headerName)
	}
	return d
}
