package model

import (
	"bytes"
	"github.com/Borislavv/advanced-cache/pkg/sort"
	"github.com/valyala/fasthttp"
	"sync"
)

var (
	kvPool = sync.Pool{
		New: func() interface{} {
			sl := make([][2][]byte, 0, 48)
			return &sl
		},
	}
	kvPoolReleaser = func(queries *[][2][]byte) {
		*queries = (*queries)[:0]
		kvPool.Put(queries)
	}
)

func (e *Entry) parseFilterAndSortQuery(b []byte) (queries *[][2][]byte, releaseFn func(*[][2][]byte)) {
	b = bytes.TrimLeft(b, "?")

	out := kvPool.Get().(*[][2][]byte)
	*out = (*out)[:0]

	type state struct {
		kIdx   int
		vIdx   int
		kFound bool
		vFound bool
	}

	s := state{}
	n := 0

	for idx, bt := range b {
		if bt == '&' {
			if s.kFound {
				var key, val []byte
				if s.vFound {
					key = b[s.kIdx : s.vIdx-1]
					val = b[s.vIdx:idx]
				} else {
					key = b[s.kIdx:idx]
					val = nil
				}

				if n < cap(*out) {
					if n < len(*out) {
						(*out)[n][0] = key
						(*out)[n][1] = val
					} else {
						*out = (*out)[:n+1]
						(*out)[n][0] = key
						(*out)[n][1] = val
					}
				} else {
					*out = append(*out, [2][]byte{key, val})
				}
				n++
			}
			s.kIdx = idx + 1
			s.kFound = true
			s.vIdx = 0
			s.vFound = false
		} else if bt == '=' && !s.vFound {
			s.vIdx = idx + 1
			s.vFound = true
		} else if !s.kFound {
			s.kIdx = idx
			s.kFound = true
		}
	}

	if s.kFound {
		var key, val []byte
		if s.vFound {
			key = b[s.kIdx : s.vIdx-1]
			val = b[s.vIdx:]
		} else {
			key = b[s.kIdx:]
			val = nil
		}

		if n < cap(*out) {
			if n < len(*out) {
				(*out)[n][0] = key
				(*out)[n][1] = val
			} else {
				*out = (*out)[:n+1]
				(*out)[n][0] = key
				(*out)[n][1] = val
			}
		} else {
			*out = append(*out, [2][]byte{key, val})
		}
		n++
	}

	*out = (*out)[:n]

	filtered := (*out)[:0]
	allowed := e.rule.Load().CacheKey.QueryBytes

	for i := 0; i < n; i++ {
		kv := (*out)[i]
		keep := false
		for _, allowedKey := range allowed {
			if bytes.HasPrefix(kv[0], allowedKey) {
				keep = true
				break
			}
		}
		if keep {
			filtered = append(filtered, kv)
		}
	}

	*out = filtered

	if len(*out) > 1 {
		sort.KVSlice(*out)
	}

	return out, kvPoolReleaser
}

func (e *Entry) getFilteredAndSortedKeyQueriesFastHttp(r *fasthttp.RequestCtx) (kvPairs *[][2][]byte, releaseFn func(*[][2][]byte)) {
	out := kvPool.Get().(*[][2][]byte)
	*out = (*out)[:0]

	allowedKeys := e.rule.Load().CacheKey.QueryBytes
	r.QueryArgs().All()(func(key, value []byte) bool {
		for _, ak := range allowedKeys {
			if bytes.HasPrefix(key, ak) {
				*out = append(*out, [2][]byte{key, value})
				break
			}
		}
		return true
	})
	if len(*out) > 1 {
		sort.KVSlice(*out)
	}

	return out, kvPoolReleaser
}

// filterAndSortKeyQueriesInPlace - filters an input slice, be careful!
func (e *Entry) filterAndSortKeyQueriesInPlace(queries *[][2][]byte) *[][2][]byte {
	q := *queries
	n := 0
	allowed := e.rule.Load().CacheKey.QueryBytes

	for i := 0; i < len(q); i++ {
		key := q[i][0]
		keep := false
		for _, ak := range allowed {
			if bytes.HasPrefix(key, ak) {
				keep = true
				break
			}
		}
		if keep {
			q[n] = q[i]
			n++
		}
	}
	if n > 1 {
		sort.KVSlice(q[:n])
	}

	*queries = q[:n]
	return queries
}
