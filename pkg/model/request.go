package model

import (
	"bytes"
	"sort"
	"sync"
	"unsafe"

	"github.com/Borislavv/traefik-http-cache-plugin/pkg/config"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/synced"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/types"
	"github.com/valyala/fasthttp"
	"github.com/zeebo/xxh3"
)

const (
	defaultPathLen   = 128
	defaultQueryLen  = 512
	defaultKeyBufLen = 1024
)

var (
	hasherPool         = &sync.Pool{New: func() any { return xxh3.New() }}
	requestBuffersPool = synced.NewBatchPool[*Request](func() *Request {
		return new(Request).clear()
	})
	requestQueryBuffersPool = synced.NewBatchPool[*types.SizedBox[[]byte]](func() *types.SizedBox[[]byte] {
		return types.NewSizedBox[[]byte](make([]byte, 0, defaultQueryLen), func(s *types.SizedBox[[]byte]) int64 {
			return int64(len(s.Value))
		})
	})
	requestHeaderBuffersPool = synced.NewBatchPool[*types.SizedBox[[]byte]](func() *types.SizedBox[[]byte] {
		return types.NewSizedBox[[]byte](make([]byte, 0, defaultQueryLen), func(s *types.SizedBox[[]byte]) int64 {
			return int64(len(s.Value))
		})
	})
	requestKeyBufferPool = synced.NewBatchPool[*types.SizedBox[[]byte]](func() *types.SizedBox[[]byte] {
		return types.NewSizedBox[[]byte](make([]byte, 0, defaultKeyBufLen), func(s *types.SizedBox[[]byte]) int64 {
			return int64(len(s.Value))
		})
	})
	requestQueryPathBuffersPool = synced.NewBatchPool[*types.SizedBox[[]byte]](func() *types.SizedBox[[]byte] {
		return types.NewSizedBox[[]byte](make([]byte, 0, defaultPathLen), func(s *types.SizedBox[[]byte]) int64 {
			return int64(len(s.Value))
		})
	})
)

type Request struct {
	cfg   *config.Cache
	key   uint64
	shard uint64
	args  *types.SizedBox[[]byte]
	path  *types.SizedBox[[]byte]
}

func NewRequestFromFasthttp(cfg *config.Cache, r *fasthttp.RequestCtx) *Request {
	sanitizeRequest(cfg, r)

	path := requestQueryPathBuffersPool.Get()
	path.Value = path.Value[:0]
	path.Value = append(path.Value, r.Path()...)

	req := requestBuffersPool.Get()
	*req = Request{
		cfg:  cfg,
		path: path,
	}

	req.setUp(r.QueryArgs(), &r.Request.Header)

	return req
}

func NewRawRequest(cfg *config.Cache, key, shard uint64, query, path []byte) *Request {

	return &Request{
		cfg:   cfg,
		key:   key,
		shard: shard,
		args: types.NewSizedBox[[]byte](query, func(s *types.SizedBox[[]byte]) int64 {
			return int64(len(s.Value))
		}),
		path: types.NewSizedBox[[]byte](path, func(s *types.SizedBox[[]byte]) int64 {
			return int64(len(s.Value))
		}),
	}
}

func NewRequest(cfg *config.Cache, path []byte, args map[string][]byte, headers map[string][][]byte) *Request {
	req := &Request{
		cfg:  cfg,
		path: path,
	}
	req.setUpManually(args, headers)
	return req
}

func (r *Request) ToQuery() []byte {
	return r.args
}

func (r *Request) Path() []byte {
	return r.path
}

func (r *Request) MapKey() uint64 {
	return r.key
}

func (r *Request) ShardKey() uint64 {
	return r.shard
}

func (r *Request) Weight() int64 {
	return int64(unsafe.Sizeof(*r)) + int64(len(r.args.Value))
}

func (r *Request) setUp(args *fasthttp.Args, header *fasthttp.RequestHeader) {
	argsBuf := requestQueryBuffersPool.Get()

	if args.Len() > 0 {
		var sortedArgs []struct {
			Key   []byte
			Value []byte
		}
		args.VisitAll(func(key, value []byte) {
			k := make([]byte, len(key))
			v := make([]byte, len(value))
			copy(k, key)
			copy(v, value)
			sortedArgs = append(sortedArgs, struct {
				Key   []byte
				Value []byte
			}{k, v})
		})

		sort.Slice(sortedArgs, func(i, j int) bool {
			return bytes.Compare(sortedArgs[i].Key, sortedArgs[j].Key) < 0
		})

		argsLength := 1 // символ '?'
		for _, p := range sortedArgs {
			argsLength += len(p.Key) + len(p.Value) + 2 // key=value&
		}

		argsBuf = make([]byte, 0, argsLength)
		argsBuf = append(argsBuf, '?')
		for _, p := range sortedArgs {
			argsBuf = append(argsBuf, p.Key...)
			argsBuf = append(argsBuf, '=')
			argsBuf = append(argsBuf, p.Value...)
			argsBuf = append(argsBuf, '&')
		}
		if len(argsBuf) > 1 {
			argsBuf = argsBuf[:len(argsBuf)-1] // убрать последний '&'
		} else {
			argsBuf = argsBuf[:0]
		}
	}
	r.args = argsBuf

	headersBuf := requestHeaderBuffersPool.Get()
	defer func() {
		headersBuf.Value = headersBuf.Value[:0]
		requestHeaderBuffersPool.Put(headersBuf)
	}()

	if header.Len() > 0 {
		var sortedHeaders []struct {
			Key   []byte
			Value []byte
		}
		header.VisitAll(func(key, value []byte) {
			k := make([]byte, len(key))
			v := make([]byte, len(value))
			copy(k, key)
			copy(v, value)
			sortedHeaders = append(sortedHeaders, struct {
				Key   []byte
				Value []byte
			}{k, v})
		})

		sort.Slice(sortedHeaders, func(i, j int) bool {
			return bytes.Compare(sortedHeaders[i].Key, sortedHeaders[j].Key) < 0
		})

		headersLength := 0
		for _, h := range sortedHeaders {
			headersLength += len(h.Key) + len(h.Value) + 2 // key:value\n
		}

		headersBuf = make([]byte, 0, headersLength)
		for _, h := range sortedHeaders {
			headersBuf = append(headersBuf, h.Key...)
			headersBuf = append(headersBuf, ':')
			headersBuf = append(headersBuf, h.Value...)
			headersBuf = append(headersBuf, '\n')
		}
		if len(headersBuf) > 0 {
			headersBuf = headersBuf[:len(headersBuf)-1] // убрать последний '\n'
		} else {
			headersBuf = headersBuf[:0]
		}
	}

	bufLen := len(argsBuf) + len(headersBuf) + len(path) + 1
	buf := make([]byte, 0, bufLen)
	if bufLen > 1 {
		buf = append(buf, path...)
		buf = append(buf, argsBuf...)
		buf = append(buf, '\n')
		buf = append(buf, headersBuf...)
	}

	r.key = hash(buf.Value)
	r.shard = sharded.MapShardKey(r.key)
}

func (r *Request) setUpManually(args map[string][]byte, headers map[string][][]byte) {
	argsLength := 1
	for key, value := range args {
		argsLength += len(key) + len(value) + 2
	}

	queryBuf := make([]byte, 0, argsLength)
	queryBuf = append(queryBuf, []byte("?")...)
	for key, value := range args {
		queryBuf = append(queryBuf, key...)
		queryBuf = append(queryBuf, []byte("=")...)
		queryBuf = append(queryBuf, value...)
		queryBuf = append(queryBuf, []byte("&")...)
	}
	if len(queryBuf) > 1 {
		queryBuf = queryBuf[:len(queryBuf)-1] // remove the last & char
	} else {
		queryBuf = queryBuf[:0] // no parameters
	}

	headersLength := 0
	for key, values := range headers {
		for _, value := range values {
			headersLength += len(key) + len(value) + 2
		}
	}

	headersBuf := make([]byte, 0, headersLength)
	for key, values := range headers {
		for _, value := range values {
			headersBuf = append(headersBuf, key...)
			headersBuf = append(headersBuf, []byte(":")...)
			headersBuf = append(headersBuf, value...)
			headersBuf = append(headersBuf, []byte("\n")...)
		}
	}
	if len(headersBuf) > 0 {
		headersBuf = headersBuf[:len(headersBuf)-1] // remove the last \n char
	} else {
		headersBuf = headersBuf[:0] // no headers
	}

	bufLen := len(queryBuf) + len(headersBuf) + 1
	buf := make([]byte, 0, bufLen)
	if bufLen > 1 {
		buf = append(buf, queryBuf...)
		buf = append(buf, []byte("\n")...)
		buf = append(buf, headersBuf...)
	}

	r.args = queryBuf
	r.key = hash(buf)
	r.shard = sharded.MapShardKey(r.key)
}

func hash(buf []byte) uint64 {
	hasher := hasherPool.Get().(*xxh3.Hasher)
	defer hasherPool.Put(hasher)

	hasher.Reset()
	if _, err := hasher.Write(buf); err != nil {
		panic(err)
	}

	return hasher.Sum64()
}

func sanitizeRequest(cfg *config.Cache, ctx *fasthttp.RequestCtx) {
	allowedQueries, allowedHeaders := getKeyAllowed(cfg, ctx.Path())
	filterKeyQueriesInPlace(ctx, allowedQueries)
	filterKeyHeadersInPlace(ctx, allowedHeaders)
}

func getKeyAllowed(cfg *config.Cache, path []byte) (queries [][]byte, headers [][]byte) {
	queries = make([][]byte, 0, 14)
	headers = make([][]byte, 0, 14)
	for _, rule := range cfg.Cache.Rules {
		if bytes.HasPrefix(path, rule.PathBytes) {
			for _, param := range rule.CacheKey.QueryBytes {
				queries = append(queries, param)
			}
			for _, header := range rule.CacheKey.HeadersBytes {
				headers = append(headers, header)
			}
		}
	}
	return queries, headers
}

var bufferPool = &sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

func filterKeyQueriesInPlace(ctx *fasthttp.RequestCtx, allowed [][]byte) {
	original := ctx.QueryArgs()
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()

	original.VisitAll(func(k, v []byte) {
		for _, ak := range allowed {
			if bytes.HasPrefix(k, ak) {
				buf.Write(k)
				buf.WriteByte('=')
				buf.Write(v)
				buf.WriteByte('&')
				break
			}
		}
	})

	if buf.Len() > 0 {
		buf.Truncate(buf.Len() - 1) // удалить последний &
		ctx.URI().SetQueryStringBytes(buf.Bytes())
	} else {
		ctx.URI().SetQueryStringBytes(nil) // удалить query
	}

	bufferPool.Put(buf)
}

func filterKeyHeadersInPlace(ctx *fasthttp.RequestCtx, allowed [][]byte) {
	headers := &ctx.Request.Header

	var filtered [][2][]byte
	headers.VisitAll(func(k, v []byte) {
		for _, ak := range allowed {
			if bytes.EqualFold(k, ak) {
				// allocate a new slice because the origin slice valid until request is alive,
				// further this "value" (slice) will be reused for new data be fasthttp (owner).
				// Don't remove allocation or will have UNDEFINED BEHAVIOR!
				kCopy := append([]byte(nil), k...)
				vCopy := append([]byte(nil), v...)
				filtered = append(filtered, [2][]byte{kCopy, vCopy})
				break
			}
		}
	})

	// Remove all headers
	headers.Reset()

	// Setting up only allowed
	for _, kv := range filtered {
		headers.SetBytesKV(kv[0], kv[1])
	}
}

// clear resets the Request to zero (except for buffer capacity).
func (r *Request) clear() *Request {
	r.key = 0
	r.shard = 0
	r.args = nil
	r.path = nil
	return r
}

// Release releases and resets the request (and any underlying buffer/tag slices) for reuse.
func (r *Request) Release() {
	requestQueryBuffersPool.Put(r.args)     // release args buffer
	requestQueryPathBuffersPool.Put(r.path) // release args path buffer

	r.clear()           // clear itself request
	RequestsPool.Put(r) // release itself request
}
