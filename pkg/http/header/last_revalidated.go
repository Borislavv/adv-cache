package header

import (
	"github.com/valyala/fasthttp"
	"sync"
	"time"
)

var (
	lastRevalidatedStrKey   = "Last-Revalidated"
	lastRevalidatedBytesKey = []byte(lastRevalidatedStrKey)
)

var (
	timePool = sync.Pool{
		New: func() any {
			return new(time.Time)
		},
	}
	bufPool = sync.Pool{
		New: func() interface{} {
			sl := make([]byte, 0, 32)
			return &sl
		},
	}
)

func SetLastRevalidatedValueFastHttp(r *fasthttp.RequestCtx, v int64) {
	if v == 0 {
		return
	}

	buf := bufPool.Get().(*[]byte)
	*buf = (*buf)[:0]

	*buf = appendLastRevalidatedHeader(buf, v)
	r.Response.Header.SetBytesKV(lastRevalidatedBytesKey, *buf)

	bufPool.Put(buf)
}

func appendLastRevalidatedHeader(dst *[]byte, unixNano int64) []byte {
	t := timePool.Get().(*time.Time)
	defer timePool.Put(t)

	*t = time.Unix(0, unixNano).UTC() // must be UTC per RFC 7231

	return t.AppendFormat(*dst, time.RFC1123)
}
