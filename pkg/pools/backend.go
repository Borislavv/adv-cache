package pools

import (
	"bytes"
	"github.com/valyala/fasthttp"
	"sync"
	"time"
)

var BackendBufPool = sync.Pool{
	New: func() interface{} {
		sl := make([]byte, 0, 32)
		return &sl
	},
}

var BackendHttpClientPool = &fasthttp.Client{
	MaxConnsPerHost: 512,
	ReadTimeout:     5 * time.Second,
	WriteTimeout:    5 * time.Second,
}

var BackendBodyBufferPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}
