package pools

import (
	"sync"
)

var (
	KeyValueSlicePool = sync.Pool{
		New: func() interface{} {
			kv := make([][2][]byte, 0, 32)
			return &kv
		},
	}
)
