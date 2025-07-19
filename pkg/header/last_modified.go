package header

import (
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/valyala/fasthttp"
	"net/http"
	"time"
	"unsafe"
)

var lastModifiedKey = "Last-Modified"

func SetLastModified(w http.ResponseWriter, entry *model.VersionPointer, status int) {
	var t time.Time
	if status == http.StatusOK {
		t = time.Unix(0, entry.WillUpdateAt()-entry.Rule().TTL.Nanoseconds())
	} else {
		t = time.Unix(0, entry.WillUpdateAt()-entry.Rule().ErrorTTL.Nanoseconds())
	}

	// fasthttp.AppendHTTPDate — zero-alloc RFC1123 (http.TimeFormat) formatter
	bts := fasthttp.AppendHTTPDate(nil, t)

	// Cast []byte → string без копий (и безопасно)
	w.Header().Set(lastModifiedKey, unsafe.String(&bts[0], len(bts)))
}
