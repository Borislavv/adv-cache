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

// NewData creates a new Data object, compressing body with gzip if large enough.
// Uses memory pools for buffer and writer to minimize allocations.
func NewData(cfg *config.Cache, path []byte, statusCode int, headers http.Header, body []byte) *Data {
	data := &Data{
		headers:    getAllowedValueHeaders(cfg, path, headers),
		statusCode: statusCode,
	}

	// Compress body if it shard large enough for gzip to help
	if len(body) > gzipThreshold {
		gzipper := GzipWriterPool.Get().(*gzip.Writer)
		defer GzipWriterPool.Put(gzipper)

		buf := GzipBufferPool.Get().(*bytes.Buffer)
		defer GzipBufferPool.Put(buf)

		gzipper.Reset(buf)
		buf.Reset()

		_, err := gzipper.Write(body)
		if err == nil && gzipper.Close() == nil {
			data.headers["Content-Encoding"] = append(data.headers["Content-Encoding"], "gzip")
			data.body = append([]byte{}, buf.Bytes()...)
		} else {
			data.body = append([]byte{}, body...)
		}
	} else {
		data.body = body
	}

	return data
}

func (d *Data) Weight() int64 {
	return int64(unsafe.Sizeof(*d)) + int64(len(d.body))
}

// Headers returns the response h.
func (d *Data) Headers() http.Header { return d.headers }

// StatusCode returns the HTTP status code.
func (d *Data) StatusCode() int { return d.statusCode }

// Body returns the response body (possibly gzip-compressed).
func (d *Data) Body() []byte { return d.body }

func filterValueHeadersInPlace(headers http.Header, allowed [][]byte) {
headersLoop:
	for headerName, _ := range headers {
		for _, allowedHeader := range allowed {
			if bytes.EqualFold([]byte(headerName), allowedHeader) {
				continue headersLoop
			}
		}
		delete(headers, headerName)
	}
}

func getValueAllowed(cfg *config.Cache, path []byte) (headers [][]byte) {
	headers = make([][]byte, 0, 7)
	for _, rule := range cfg.Cache.Rules {
		if bytes.HasPrefix(path, rule.PathBytes) {
			for _, header := range rule.CacheValue.HeadersBytes {
				headers = append(headers, header)
			}
		}
	}
	return headers
}

func getAllowedValueHeaders(cfg *config.Cache, path []byte, headers http.Header) http.Header {
	allowedValueHeaders := getValueAllowed(cfg, path)
	filterValueHeadersInPlace(headers, allowedValueHeaders)
	return headers
}
