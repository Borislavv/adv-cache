package gzip

import (
	"bytes"
	"compress/gzip"
	"io"
	"sync"
	"time"
)

const defaultLevel = gzip.BestSpeed

var writerPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, defaultLevel)
		return w
	},
}

var bufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// normalizeHeader resets gzip header to a deterministic state.
func normalizeHeader(gw *gzip.Writer) {
	gw.Header = gzip.Header{}
	gw.Header.ModTime = time.Unix(0, 0)
}

// AcquireWriter returns a pooled *gzip.Writer reset to write into w.
func AcquireWriter(w io.Writer) *gzip.Writer {
	gw := writerPool.Get().(*gzip.Writer)
	gw.Reset(w)
	normalizeHeader(gw)
	return gw
}

// ReleaseWriter closes the writer (flushing final block) and returns it to the pool.
func ReleaseWriter(gw *gzip.Writer) error {
	err := gw.Close()
	writerPool.Put(gw)
	return err
}

// AcquireBuffer returns a pooled *bytes.Buffer with length 0.
func AcquireBuffer() *bytes.Buffer {
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

// ReleaseBuffer returns the buffer to the pool.
func ReleaseBuffer(buf *bytes.Buffer) {
	bufferPool.Put(buf)
}

// EncodeToWriter compresses p and writes the result to w using a pooled writer.
func EncodeToWriter(w io.Writer, p []byte) error {
	gw := AcquireWriter(w)
	_, err := gw.Write(p)
	if err != nil {
		_ = ReleaseWriter(gw)
		return err
	}
	return ReleaseWriter(gw)
}

// EncodeToBuffer compresses p and returns a pooled buffer holding the result.
// The caller must call ReleaseBuffer on the returned buffer when done.
func EncodeToBuffer(p []byte) (*bytes.Buffer, error) {
	buf := AcquireBuffer()
	gw := AcquireWriter(buf)

	_, err := gw.Write(p)
	if err != nil {
		_ = ReleaseWriter(gw)
		ReleaseBuffer(buf)
		return nil, err
	}
	if err = ReleaseWriter(gw); err != nil {
		ReleaseBuffer(buf)
		return nil, err
	}
	return buf, nil
}

// Encode compresses p and returns a standalone []byte.
// This is convenient but performs one allocation to detach from the pooled buffer.
func Encode(p []byte) ([]byte, error) {
	buf := AcquireBuffer()
	gw := AcquireWriter(buf)

	_, err := gw.Write(p)
	if err != nil {
		_ = ReleaseWriter(gw)
		ReleaseBuffer(buf)
		return nil, err
	}
	if err = ReleaseWriter(gw); err != nil {
		ReleaseBuffer(buf)
		return nil, err
	}

	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	ReleaseBuffer(buf)
	return out, nil
}
