package model

import (
	"bytes"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/config"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/valyala/fasthttp"
	"github.com/zeebo/xxh3"
	"sync"
	"unsafe"
)

var hasherPool = &sync.Pool{New: func() any { return xxh3.New() }}

type Request struct {
	cfg   *config.Cache
	key   uint64
	shard uint64
	query []byte
	path  []byte
}

func NewRequestFromFasthttp(cfg *config.Cache, r *fasthttp.RequestCtx) *Request {
	sanitizeRequest(cfg, r)
	req := &Request{
		cfg:  cfg,
		path: r.Path(),
	}
	req.setUp(r.Path(), r.QueryArgs(), &r.Request.Header)
	return req
}

func NewRawRequest(cfg *config.Cache, key, shard uint64, query, path []byte) *Request {
	return &Request{
		cfg:   cfg,
		key:   key,
		shard: shard,
		query: query,
		path:  path,
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
	return r.query
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
	return int64(unsafe.Sizeof(*r)) + int64(len(r.query))
}

func (r *Request) setUp(path []byte, args *fasthttp.Args, header *fasthttp.RequestHeader) {
	var argsBuf []byte
	if args.Len() > 0 {
		argsLength := 1
		args.VisitAll(func(key, value []byte) {
			argsLength += len(key) + len(value) + 2
		})

		argsBuf = make([]byte, 0, argsLength)
		argsBuf = append(argsBuf, []byte("?")...)
		args.VisitAll(func(key, value []byte) {
			copiedValue := make([]byte, len(value))
			// allocate a new slice because the origin slice valid only during request is alive,
			// further this "value" slice will be reused for new data be fasthttp (owner).
			// Don't remove allocation or will have UNDEFINED BEHAVIOR!
			copy(copiedValue, value)
			argsBuf = append(argsBuf, key...)
			argsBuf = append(argsBuf, []byte("=")...)
			argsBuf = append(argsBuf, value...)
			argsBuf = append(argsBuf, []byte("&")...)
		})
		if len(argsBuf) > 1 {
			argsBuf = argsBuf[:len(argsBuf)-1] // remove the last & char
		} else {
			argsBuf = argsBuf[:0] // no parameters
		}
	}

	var headersBuf []byte
	if header.Len() > 0 {
		headersLength := 0
		header.VisitAll(func(key, value []byte) {
			headersLength += len(key) + len(value) + 2
		})

		headersBuf = make([]byte, 0, headersLength)
		header.VisitAll(func(key, value []byte) {
			headersBuf = append(headersBuf, key...)
			headersBuf = append(headersBuf, []byte(":")...)
			headersBuf = append(headersBuf, value...)
			headersBuf = append(headersBuf, []byte("\n")...)
		})
		if len(headersBuf) > 0 {
			headersBuf = headersBuf[:len(headersBuf)-1] // remove the last \n char
		} else {
			headersBuf = headersBuf[:0] // no headers
		}
	}

	bufLen := len(argsBuf) + len(headersBuf) + len(path) + 1
	buf := make([]byte, 0, bufLen)
	if bufLen > 1 {
		buf = append(buf, path...)
		buf = append(buf, argsBuf...)
		buf = append(buf, []byte("\n")...)
		buf = append(buf, headersBuf...)
	}

	r.query = argsBuf
	r.key = hash(buf)
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

	r.query = queryBuf
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

func filterKeyQueriesInPlace(ctx *fasthttp.RequestCtx, allowed [][]byte) {
	args := ctx.QueryArgs()
	result := fasthttp.AcquireArgs()

	args.VisitAll(func(k, v []byte) {
		for _, ak := range allowed {
			if bytes.HasPrefix(k, ak) {
				result.AddBytesKV(k, v)
				break
			}
		}
	})

	ctx.URI().SetQueryString(result.String())
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
