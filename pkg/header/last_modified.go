package header

import (
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/valyala/fasthttp"
	"net/http"
	"sync"
	"time"
	"unsafe"
)

var (
	lastModifiedStrKey   = "Last-Modified"
	lastModifiedBytesKey = []byte(lastModifiedStrKey)
)

var timePool = sync.Pool{
	New: func() any {
		return new(time.Time)
	},
}

func SetLastModifiedNetHttp(w http.ResponseWriter, entry *model.VersionPointer, status int) {
	t := timePool.Get().(*time.Time)
	defer timePool.Put(t)

	if status == http.StatusOK {
		*t = time.Unix(0, entry.WillUpdateAt()-entry.Rule().TTL.Nanoseconds())
	} else {
		*t = time.Unix(0, entry.WillUpdateAt()-entry.Rule().ErrorTTL.Nanoseconds())
	}

	bts := fasthttp.AppendHTTPDate(nil, *t)
	w.Header().Set(lastModifiedStrKey, unsafe.String(&bts[0], len(bts)))
}

func SetLastModifiedFastHttp(r *fasthttp.RequestCtx, entry *model.VersionPointer, status int) {
	t := timePool.Get().(*time.Time)
	defer timePool.Put(t)

	if status == http.StatusOK {
		*t = time.Unix(0, entry.WillUpdateAt()-entry.Rule().TTL.Nanoseconds())
	} else {
		*t = time.Unix(0, entry.WillUpdateAt()-entry.Rule().ErrorTTL.Nanoseconds())
	}

	buf := fasthttp.AppendHTTPDate(nil, *t)
	r.Response.Header.SetBytesKV(lastModifiedBytesKey, buf)
}
