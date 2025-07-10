package pools

import "sync"

var (
	MockQueryBuffPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, 512)
		},
	}
)
