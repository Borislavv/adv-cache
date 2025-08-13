package brotli

import (
	"bytes"
	"io"
	"sync"

	"github.com/andybalholm/brotli"
)

const defaultQuality = 1

var writerPool = sync.Pool{
	New: func() any {
		// Writer is created once with defaultQuality; quality remains unchanged between uses.
		return brotli.NewWriterLevel(io.Discard, defaultQuality)
	},
}

var bufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// AcquireWriter returns a pooled *brotli.Writer reset to write into w.
func AcquireWriter(w io.Writer) *brotli.Writer {
	bw := writerPool.Get().(*brotli.Writer)
	bw.Reset(w)
	return bw
}

// ReleaseWriter closes the writer (flushing final block) and returns it to the pool.
func ReleaseWriter(bw *brotli.Writer) error {
	err := bw.Close()
	writerPool.Put(bw)
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
// This path avoids allocations in hot code paths.
func EncodeToWriter(w io.Writer, p []byte) error {
	bw := AcquireWriter(w)
	_, err := bw.Write(p)
	if err != nil {
		_ = ReleaseWriter(bw)
		return err
	}
	return ReleaseWriter(bw)
}

// EncodeToBuffer compresses p and returns a pooled buffer holding the result.
// The caller must call ReleaseBuffer on the returned buffer when done.
// No copy is made; safest for performance-critical paths where the caller controls lifetimes.
func EncodeToBuffer(p []byte) (*bytes.Buffer, error) {
	buf := AcquireBuffer()
	bw := AcquireWriter(buf)

	_, err := bw.Write(p)
	if err != nil {
		_ = ReleaseWriter(bw)
		ReleaseBuffer(buf)
		return nil, err
	}
	if err = ReleaseWriter(bw); err != nil {
		ReleaseBuffer(buf)
		return nil, err
	}
	return buf, nil
}

// Encode compresses p and returns a standalone []byte.
// This is convenient but performs one allocation to detach from the pooled buffer.
func Encode(p []byte) ([]byte, error) {
	buf := AcquireBuffer()
	bw := AcquireWriter(buf)

	_, err := bw.Write(p)
	if err != nil {
		_ = ReleaseWriter(bw)
		ReleaseBuffer(buf)
		return nil, err
	}
	if err = ReleaseWriter(bw); err != nil {
		ReleaseBuffer(buf)
		return nil, err
	}

	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	ReleaseBuffer(buf)
	return out, nil
}
