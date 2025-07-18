package header

import (
	"github.com/Borislavv/advanced-cache/pkg/model"
	"net/http"
	"time"
	"unsafe"
)

var lastModifiedKey = "Last-Modified"

func SetLastModified(w http.ResponseWriter, entry *model.VersionPointer, status int) {
	if status == http.StatusOK {
		bts := time.Unix(0, entry.WillUpdateAt()-entry.Rule().TTL.Nanoseconds()).AppendFormat(nil, http.TimeFormat)
		w.Header().Set(lastModifiedKey, unsafe.String(unsafe.SliceData(bts), len(bts)))
	} else {
		bts := time.Unix(0, entry.WillUpdateAt()-entry.Rule().ErrorTTL.Nanoseconds()).AppendFormat(nil, http.TimeFormat)
		w.Header().Set(lastModifiedKey, unsafe.String(unsafe.SliceData(bts), len(bts)))
	}
}
