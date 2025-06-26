package model

import (
	"bytes"
	"errors"
	"github.com/Borislavv/advanced-cache/pkg/config"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/valyala/fasthttp"
	"github.com/zeebo/xxh3"
	"net/http"
	"sort"
	"strings"
	"sync"
	"unsafe"
)

var (
	hasherPool        = &sync.Pool{New: func() any { return xxh3.New() }}
	RuleNotFoundError = errors.New("rule not found")
)

type Request struct {
	rule  *config.Rule // possibly nil pointer (be careful)
	key   uint64
	shard uint64
	query []byte
	path  []byte
}

func NewRequestFromNetHttp(cfg *config.Cache, r *http.Request) (*Request, error) {
	// path must be a readonly slice, don't change it anywhere
	req := &Request{path: unsafe.Slice(unsafe.StringData(r.URL.Path), len(r.URL.Path))} // static value (strings are immutable, so easily refer to it)

	rule := matchRule(cfg, req.path)
	if rule == nil {
		return nil, RuleNotFoundError
	}

	req.rule = rule

	queries := getFilteredKeyQueriesNetHttp(r, rule.CacheKey.QueryBytes)
	headers := getFilteredKeyHeadersNetHttp(r, rule.CacheKey.HeadersBytes)

	req.setUpManually(queries, headers)

	return req, nil
}

func NewRequestFromFasthttp(cfg *config.Cache, r *fasthttp.RequestCtx) (*Request, error) {
	// full separated slice bytes of path a safe for changes due to it copy
	path := append([]byte(nil), r.Path()...) // path in the fasthttp are reusable resource, so just copy it

	req := &Request{path: path}

	rule := matchRule(cfg, path)
	if rule == nil {
		return nil, RuleNotFoundError
	}

	req.rule = rule
	sanitizeRequest(rule, r)

	req.setUp(path, r.QueryArgs(), &r.Request.Header)

	return req, nil
}

func NewRawRequest(cfg *config.Cache, key, shard uint64, query, path []byte) *Request {
	return &Request{key: key, shard: shard, query: query, path: path, rule: matchRule(cfg, path)}
}

func NewRequest(cfg *config.Cache, path []byte, argsKvPairs [][2][]byte, headersKvPairs [][2][]byte) *Request {
	req := &Request{path: path}
	rule := matchRule(cfg, path)
	if rule != nil {
		req.rule = rule
	}
	req.setUpManually(argsKvPairs, headersKvPairs)
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

func (r *Request) setUpManually(argsKvPairs [][2][]byte, headersKvPairs [][2][]byte) {
	argsLength := 1
	for _, pair := range argsKvPairs {
		argsLength += len(pair[0]) + len(pair[1]) + 2
	}

	queryBuf := make([]byte, 0, argsLength)
	queryBuf = append(queryBuf, []byte("?")...)
	for _, pair := range argsKvPairs {
		queryBuf = append(queryBuf, pair[0]...)
		queryBuf = append(queryBuf, []byte("=")...)
		queryBuf = append(queryBuf, pair[1]...)
		queryBuf = append(queryBuf, []byte("&")...)
	}
	if len(queryBuf) > 1 {
		queryBuf = queryBuf[:len(queryBuf)-1] // remove the last & char
	} else {
		queryBuf = queryBuf[:0] // no parameters
	}

	headersLength := 0
	for _, pair := range headersKvPairs {
		headersLength += len(pair[0]) + len(pair[1]) + 2
	}

	headersBuf := make([]byte, 0, headersLength)
	for _, pair := range headersKvPairs {
		headersBuf = append(headersBuf, pair[0]...)
		headersBuf = append(headersBuf, []byte(":")...)
		headersBuf = append(headersBuf, pair[1]...)
		headersBuf = append(headersBuf, []byte("\n")...)
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
	if len(allowed) == 0 {
		return
	}

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
	}
}

func filterKeyHeadersInPlace(ctx *fasthttp.RequestCtx, allowed [][]byte) {
	if len(allowed) == 0 {
		return
	}

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

	if len(filtered) > 0 {
		// Remove all headers
		headers.Reset()

		// Setting up only allowed
		for _, kv := range filtered {
			headers.SetBytesKV(kv[0], kv[1])
		}
	}
}

func getFilteredKeyQueriesNetHttp(r *http.Request, allowed [][]byte) (kvPairs [][2][]byte) {
	var filtered = make([][2][]byte, 0, len(allowed))
	if len(allowed) == 0 {
		return filtered
	}
	for _, pair := range strings.Split(r.URL.RawQuery, "&") {
		if eqIndex := strings.Index(pair, "="); eqIndex != -1 {
			key := pair[:eqIndex]
			keyBytes := unsafe.Slice(unsafe.StringData(key), len(key))
			value := pair[eqIndex+1:]
			valueBytes := unsafe.Slice(unsafe.StringData(value), len(value))
			for _, allowedKey := range allowed {
				if bytes.HasPrefix(keyBytes, allowedKey) {
					filtered = append(filtered, [2][]byte{keyBytes, valueBytes})
					break
				}
			}
		}
	}
	return filtered
}

func getFilteredKeyHeadersNetHttp(r *http.Request, allowed [][]byte) (kvPairs [][2][]byte) {
	var filtered = make([][2][]byte, 0, len(allowed))
	if len(allowed) == 0 {
		return
	}
	for key, values := range r.Header {
		for _, value := range values {
			keyBytes := unsafe.Slice(unsafe.StringData(key), len(key))
			valueBytes := unsafe.Slice(unsafe.StringData(value), len(value))
			for _, allowedKey := range allowed {
				if bytes.EqualFold(keyBytes, allowedKey) {
					filtered = append(filtered, [2][]byte{keyBytes, valueBytes})
					break
				}
			}
		}
	}
	return filtered
}
