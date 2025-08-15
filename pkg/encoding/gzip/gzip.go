package gz

import (
	"bytes"
	cgzip "compress/gzip"
	"io"
	"sync"
	"time"
)

const defaultLevel = cgzip.BestSpeed

var writerPool = sync.Pool{
	New: func() any {
		w, _ := cgzip.NewWriterLevel(io.Discard, defaultLevel)
		return w
	},
}

var bufferPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// normalizeHeader resets gzip header to a deterministic state to avoid leaking
// Name/Comment/Extra/ModTime across pool uses and to make output stable.
func normalizeHeader(gw *cgzip.Writer) {
	gw.Header = cgzip.Header{
		ModTime: time.Time{}, // zero time (00:00:00 UTC, year 1)
		OS:      255,         // unknown; avoids platform-dependent byte
	}
}

// AcquireWriter returns a pooled *gzip.Writer reset to write into w.
func AcquireWriter(w io.Writer) *cgzip.Writer {
	gw := writerPool.Get().(*cgzip.Writer)
	gw.Reset(w)
	normalizeHeader(gw)
	return gw
}

// ReleaseWriter closes the writer (flushes) and returns it to the pool.
func ReleaseWriter(gw *cgzip.Writer) error {
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
func ReleaseBuffer(buf *bytes.Buffer) { bufferPool.Put(buf) }

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
func EncodeToBuffer(p []byte) (*bytes.Buffer, error) {
	buf := AcquireBuffer()
	gw := AcquireWriter(buf)

	if _, err := gw.Write(p); err != nil {
		_ = ReleaseWriter(gw)
		ReleaseBuffer(buf)
		return nil, err
	}
	if err := ReleaseWriter(gw); err != nil {
		ReleaseBuffer(buf)
		return nil, err
	}
	return buf, nil
}

// Encode compresses p and returns a detached []byte.
func Encode(p []byte) ([]byte, error) {
	buf := AcquireBuffer()
	gw := AcquireWriter(buf)

	if _, err := gw.Write(p); err != nil {
		_ = ReleaseWriter(gw)
		ReleaseBuffer(buf)
		return nil, err
	}
	if err := ReleaseWriter(gw); err != nil {
		ReleaseBuffer(buf)
		return nil, err
	}

	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	ReleaseBuffer(buf)
	return out, nil
}
