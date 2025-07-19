package gzipper

import (
	"compress/gzip"
	"io"
	"sync"
)

var readerPool = sync.Pool{
	New: func() any {
		return &gzip.Reader{}
	},
}

var writerPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(io.Discard)
	},
}

// AcquireReader returns a reusable gzip.Reader
func AcquireReader(r io.Reader) (*gzip.Reader, error) {
	gr := readerPool.Get().(*gzip.Reader)
	if err := gr.Reset(r); err != nil {
		// corrupted, fallback to new instance
		return gzip.NewReader(r)
	}
	return gr, nil
}

// ReleaseReader returns the gzip.Reader to pool
func ReleaseReader(r *gzip.Reader) {
	r.Close()
	readerPool.Put(r)
}

// AcquireWriter returns a reusable gzip.Writer
func AcquireWriter(w io.Writer) *gzip.Writer {
	gw := writerPool.Get().(*gzip.Writer)
	gw.Reset(w)
	return gw
}

// ReleaseWriter flushes and returns writer to pool
func ReleaseWriter(w *gzip.Writer) error {
	if err := w.Close(); err != nil {
		return err
	}
	writerPool.Put(w)
	return nil
}
