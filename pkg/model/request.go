package model

import (
	"bytes"
	"github.com/Borislavv/advanced-cache/pkg/config"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/valyala/fasthttp"
	"github.com/zeebo/xxh3"
	"net/http"
	"sort"
	"sync"
	"unsafe"
)

var hasherPool = &sync.Pool{New: func() any { return xxh3.New() }}

type Request struct {
	rule  *config.Rule // possibly nil pointer (be careful)
	key   uint64
	shard uint64
	query []byte
	path  []byte
}

func NewRequestFromNetHttp(cfg *config.Cache, r *http.Request) *Request {
	path := append([]byte(nil), r.URL.Path...)

	req := &Request{path: path}

	rule := matchRule(cfg, path)
	if rule != nil {
		req.rule = rule
		sanitizeNetHttpRequest(rule, r)
	}

	setUpNetHttp(req, path, r)

	return req
}

func sanitizeNetHttpRequest(rule *config.Rule, r *http.Request) {
	queries := r.URL.Query()
	for key := range queries {
		if !keyAllowed(rule.CacheKey.QueryBytes, key) {
			queries.Del(key)
		}
	}
	r.URL.RawQuery = queries.Encode()

	for key := range r.Header {
		if !keyAllowed(rule.CacheKey.HeadersBytes, key) {
			r.Header.Del(key)
		}
	}
}

func keyAllowed(allowed [][]byte, key string) bool {
	keyBytes := []byte(key)
	for _, allowedKey := range allowed {
		if bytes.EqualFold(keyBytes, allowedKey) {
			return true
		}
	}
	return false
}

func setUpNetHttp(r *Request, path []byte, req *http.Request) {
	queries := req.URL.Query()

	var sortedArgs []struct{ Key, Value string }
	argsLength := 1
	for key, vals := range queries {
		for _, val := range vals {
			sortedArgs = append(sortedArgs, struct{ Key, Value string }{key, val})
			argsLength += len(key) + len(val) + 2
		}
	}

	sort.Slice(sortedArgs, func(i, j int) bool {
		return sortedArgs[i].Key < sortedArgs[j].Key
	})

	argsBuf := make([]byte, 0, argsLength)
	if len(sortedArgs) > 0 {
		argsBuf = append(argsBuf, '?')
		for _, arg := range sortedArgs {
			argsBuf = append(argsBuf, arg.Key...)
			argsBuf = append(argsBuf, '=')
			argsBuf = append(argsBuf, arg.Value...)
			argsBuf = append(argsBuf, '&')
		}
		argsBuf = argsBuf[:len(argsBuf)-1] // remove trailing '&'
	}

	sortedHeaders := make([]struct{ Key, Value string }, 0, len(req.Header))
	headersLength := 0
	for key, vals := range req.Header {
		for _, val := range vals {
			sortedHeaders = append(sortedHeaders, struct{ Key, Value string }{key, val})
			headersLength += len(key) + len(val) + 2
		}
	}

	sort.Slice(sortedHeaders, func(i, j int) bool {
		return sortedHeaders[i].Key < sortedHeaders[j].Key
	})

	headersBuf := make([]byte, 0, headersLength)
	for _, hdr := range sortedHeaders {
		headersBuf = append(headersBuf, hdr.Key...)
		headersBuf = append(headersBuf, ':')
		headersBuf = append(headersBuf, hdr.Value...)
		headersBuf = append(headersBuf, '\n')
	}
	if len(headersBuf) > 0 {
		headersBuf = headersBuf[:len(headersBuf)-1] // remove trailing '\n'
	}

	bufLen := len(path) + len(argsBuf) + len(headersBuf) + 1
	buf := make([]byte, 0, bufLen)
	if bufLen > 1 {
		buf = append(buf, path...)
		buf = append(buf, argsBuf...)
		buf = append(buf, '\n')
		buf = append(buf, headersBuf...)
	}

	r.query = argsBuf
	r.key = hash(buf)
	r.shard = sharded.MapShardKey(r.key)
}

func NewRequestFromFasthttp(cfg *config.Cache, r *fasthttp.RequestCtx) *Request {
	path := append([]byte(nil), r.Path()...)

	req := &Request{path: path}

	rule := matchRule(cfg, path)
	if rule != nil {
		req.rule = rule
		sanitizeRequest(rule, r)
	}

	req.setUp(path, r.QueryArgs(), &r.Request.Header)
	return req
}

func NewRawRequest(cfg *config.Cache, key, shard uint64, query, path []byte) *Request {
	req := &Request{key: key, shard: shard, query: query, path: path}
	rule := matchRule(cfg, path)
	if rule != nil {
		req.rule = rule
	}
	return req
}

func NewRequest(cfg *config.Cache, path []byte, args map[string][]byte, headers map[string][][]byte) *Request {
	req := &Request{path: path}
	rule := matchRule(cfg, path)
	if rule != nil {
		req.rule = rule
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
		argsLength := 1 // символ '?'

		var sortedArgs = make([]struct {
			Key   []byte
			Value []byte
		}, 0, args.Len())

		args.VisitAll(func(key, value []byte) {
			k := make([]byte, len(key))
			v := make([]byte, len(value))
			copy(k, key)
			copy(v, value)
			sortedArgs = append(sortedArgs, struct {
				Key   []byte
				Value []byte
			}{k, v})
			argsLength += len(k) + len(v) + 2 // key=value&
		})

		sort.Slice(sortedArgs, func(i, j int) bool {
			return bytes.Compare(sortedArgs[i].Key, sortedArgs[j].Key) < 0
		})

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

	var headersBuf []byte
	if header.Len() > 0 {
		headersLength := 0

		var sortedHeaders = make([]struct {
			Key   []byte
			Value []byte
		}, 0, header.Len())

		header.VisitAll(func(key, value []byte) {
			k := make([]byte, len(key))
			v := make([]byte, len(value))
			copy(k, key)
			copy(v, value)
			sortedHeaders = append(sortedHeaders, struct {
				Key   []byte
				Value []byte
			}{k, v})
			headersLength += len(k) + len(v) + 2
		})

		sort.Slice(sortedHeaders, func(i, j int) bool {
			return bytes.Compare(sortedHeaders[i].Key, sortedHeaders[j].Key) < 0
		})

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

func sanitizeRequest(rule *config.Rule, ctx *fasthttp.RequestCtx) {
	filterKeyQueriesInPlace(ctx, rule.CacheKey.QueryBytes)
	filterKeyHeadersInPlace(ctx, rule.CacheKey.HeadersBytes)
}

func matchRule(cfg *config.Cache, path []byte) *config.Rule {
	for _, rule := range cfg.Cache.Rules {
		if bytes.HasPrefix(path, rule.PathBytes) {
			return rule
		}
	}
	return nil
}

func filterKeyQueriesInPlace(ctx *fasthttp.RequestCtx, allowed [][]byte) {
	buf := make([]byte, 0, len(ctx.URI().QueryString()))

	ctx.QueryArgs().VisitAll(func(k, v []byte) {
		for _, ak := range allowed {
			if bytes.HasPrefix(k, ak) {
				buf = append(buf, k...)
				buf = append(buf, '=')
				buf = append(buf, v...)
				buf = append(buf, '&')
				break
			}
		}
	})

	if len(buf) > 0 {
		buf = buf[:len(buf)-1] // remove the last &
		ctx.URI().SetQueryStringBytes(buf)
	} else {
		ctx.URI().SetQueryStringBytes(nil) // удалить query
	}
}

func filterKeyHeadersInPlace(ctx *fasthttp.RequestCtx, allowed [][]byte) {
	headers := &ctx.Request.Header

	var filtered = make([][2][]byte, 0, len(allowed))
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
