package br

import (
	"bytes"
	"io"
	"sync"

	abrotli "github.com/andybalholm/brotli"
)

const defaultQuality = 1

var writerPool = sync.Pool{
	New: func() any {
		// Quality is fixed at creation time and persists across Reset.
		return abrotli.NewWriterLevel(io.Discard, defaultQuality)
	},
}

var bufferPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// AcquireWriter returns a pooled *brotli.Writer reset to write into w.
func AcquireWriter(w io.Writer) *abrotli.Writer {
	bw := writerPool.Get().(*abrotli.Writer)
	bw.Reset(w)
	return bw
}

// ReleaseWriter closes the writer (flushes) and returns it to the pool.
func ReleaseWriter(bw *abrotli.Writer) error {
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
func ReleaseBuffer(buf *bytes.Buffer) { bufferPool.Put(buf) }

// EncodeToWriter compresses p and writes the result to w using a pooled writer.
func EncodeToWriter(w io.Writer, p []byte) error {
	bw := AcquireWriter(w)
	_, err := bw.Write(p)
	if err != nil {
		_ = ReleaseWriter(bw) // ensure pooled even on error
		return err
	}
	return ReleaseWriter(bw)
}

// EncodeToBuffer compresses p and returns a pooled buffer holding the result.
func EncodeToBuffer(p []byte) (*bytes.Buffer, error) {
	buf := AcquireBuffer()
	bw := AcquireWriter(buf)

	if _, err := bw.Write(p); err != nil {
		_ = ReleaseWriter(bw)
		ReleaseBuffer(buf)
		return nil, err
	}
	if err := ReleaseWriter(bw); err != nil {
		ReleaseBuffer(buf)
		return nil, err
	}
	return buf, nil
}

// Encode compresses p and returns a detached []byte.
func Encode(p []byte) ([]byte, error) {
	buf := AcquireBuffer()
	bw := AcquireWriter(buf)

	if _, err := bw.Write(p); err != nil {
		_ = ReleaseWriter(bw)
		ReleaseBuffer(buf)
		return nil, err
	}
	if err := ReleaseWriter(bw); err != nil {
		ReleaseBuffer(buf)
		return nil, err
	}

	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	ReleaseBuffer(buf)
	return out, nil
}
