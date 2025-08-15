package pools

import "sync"

var (
	SliceBytesPool = sync.Pool{
		New: func() interface{} {
			kv := make([][2][]byte, 0, 32)
			return &kv
		},
	}
	SliceKeyValueBytesPool = sync.Pool{
		New: func() interface{} {
			kv := make([][2][]byte, 0, 32)
			return &kv
		},
	}
)
