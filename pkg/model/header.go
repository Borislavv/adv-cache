package model

import (
	"github.com/Borislavv/advanced-cache/pkg/pools"
	"github.com/Borislavv/advanced-cache/pkg/sort"
	"github.com/valyala/fasthttp"
)

func (e *Entry) getFilteredAndSortedKeyHeadersCtxFasthttp(ctx *fasthttp.RequestCtx) (kvPairs *[][2][]byte, releaseFn func(*[][2][]byte)) {
	out := pools.SliceKeyValueBytesPool.Get().(*[][2][]byte)
	*out = (*out)[:0]

	for key, keyBytes := range e.rule.Load().CacheKey.HeadersMap {
		if valueBytes := ctx.Request.Header.Peek(key); len(valueBytes) > 0 {
			*out = append(*out, [2][]byte{keyBytes, valueBytes})
		}
	}
	if len(*out) > 1 {
		sort.KVSlice(*out)
	}

	return out, kvPoolReleaser
}

func (e *Entry) getFilteredAndSortedKeyHeadersFasthttp(r *fasthttp.Request) (kvPairs *[][2][]byte, releaseFn func(*[][2][]byte)) {
	out := pools.SliceKeyValueBytesPool.Get().(*[][2][]byte)
	*out = (*out)[:0]

	for key, keyBytes := range e.rule.Load().CacheKey.HeadersMap {
		if valueBytes := r.Header.Peek(key); len(valueBytes) > 0 {
			*out = append(*out, [2][]byte{keyBytes, valueBytes})
		}
	}
	if len(*out) > 1 {
		sort.KVSlice(*out)
	}

	return out, kvPoolReleaser
}
