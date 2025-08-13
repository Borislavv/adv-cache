package bytes

import (
	"bytes"
	"github.com/zeebo/xxh3"
)

func IsBytesAreEquals(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) < 32 {
		return bytes.Equal(a, b)
	}

	ha := xxh3.Hash(a[:8]) ^ xxh3.Hash(a[len(a)/2:len(a)/2+8]) ^ xxh3.Hash(a[len(a)-8:])
	hb := xxh3.Hash(b[:8]) ^ xxh3.Hash(b[len(b)/2:len(b)/2+8]) ^ xxh3.Hash(b[len(b)-8:])
	return ha == hb
}
