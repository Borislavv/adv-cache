package pools

import (
	"sync"
)

var (
	EntryQueryHeadersPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 32)
		},
	}
	KeyValueSlicePool = sync.Pool{
		New: func() interface{} {
			return make([][2][]byte, 0, 8)
		},
	}
)
