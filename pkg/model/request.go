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
	rule        *config.Rule // possibly nil pointer (be careful)
	key         uint64
	shard       uint64
	OriginQuery []byte
	query       []byte
	path        []byte
	headers     [][2][]byte
}

func NewRequestFromNetHttp(cfg *config.Cache, r *http.Request) (*Request, error) {
	// path must be a readonly slice, don't change it anywhere
	req := &Request{path: unsafe.Slice(unsafe.StringData(r.URL.Path), len(r.URL.Path))} // static value (strings are immutable, so easily refer to it)

	rule := matchRule(cfg, req.path)
	if rule == nil {
		return nil, RuleNotFoundError
	}
	req.rule = rule

	queries := getFilteredAndSortedKeyQueriesNetHttp(r, rule.CacheKey.QueryBytes)
	headers := getFilteredAndSortedKeyHeadersNetHttp(r, rule.CacheKey.HeadersBytes)

	req.setUpManually(queries, headers)

	return req, nil
}

func NewRequestFromFasthttp(cfg *config.Cache, r *fasthttp.RequestCtx) (*Request, error) {
	// full separated slice bytes of path a safe for changes due to it copy
	path := append([]byte(nil), r.Path()...) // path in the fasthttp are reusable resource, so just copy it

	req := &Request{path: path}

	req.rule = matchRule(cfg, path)
	if req.rule == nil {
		return nil, RuleNotFoundError
	}

	queries := getFilteredAndSortedKeyQueriesFastHttp(r, req.rule.CacheKey.QueryBytes)
	headers := getFilteredAndSortedKeyHeadersFastHttp(&r.Request.Header, req.rule.CacheKey.HeadersBytes)

	req.setUpManually(queries, headers)

	return req, nil
}

func NewRawRequest(cfg *config.Cache, key, shard uint64, query, path []byte, headers [][2][]byte) *Request {
	return &Request{key: key, shard: shard, query: query, path: path, headers: headers, rule: matchRule(cfg, path)}
}

func NewRequest(cfg *config.Cache, path []byte, argsKvPairs [][2][]byte, headersKvPairs [][2][]byte) *Request {
	req := &Request{path: path, rule: matchRule(cfg, path)}
	req.setUpManually(argsKvPairs, headersKvPairs)
	return req
}

func (r *Request) ToQuery() []byte {
	return r.query
}

func (r *Request) Headers() [][2][]byte {
	return r.headers
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

func (r *Request) setUpManually(argsKvPairs [][2][]byte, headersKvPairs [][2][]byte) {
	r.headers = headersKvPairs

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

	r.query = queryBuf[:]
	r.OriginQuery = append(r.OriginQuery, queryBuf[:]...)
	r.key = hash(buf[:])
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

func matchRule(cfg *config.Cache, path []byte) *config.Rule {
	for _, rule := range cfg.Cache.Rules {
		if bytes.HasPrefix(path, rule.PathBytes) {
			return rule
		}
	}
	return nil
}

func getFilteredAndSortedKeyQueriesNetHttp(r *http.Request, allowed [][]byte) (kvPairs [][2][]byte) {
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
	sort.Slice(filtered, func(i, j int) bool {
		return bytes.Compare(filtered[i][0], filtered[j][0]) < 0
	})
	return filtered
}

func getFilteredAndSortedKeyHeadersNetHttp(r *http.Request, allowed [][]byte) (kvPairs [][2][]byte) {
	var filtered = make([][2][]byte, 0, len(allowed))
	if len(allowed) == 0 {
		return filtered
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
	sort.Slice(filtered, func(i, j int) bool {
		return bytes.Compare(filtered[i][0], filtered[j][0]) < 0
	})
	return filtered
}

func getFilteredAndSortedKeyQueriesFastHttp(ctx *fasthttp.RequestCtx, allowed [][]byte) (kvPairs [][2][]byte) {
	var filtered = make([][2][]byte, 0, len(allowed))
	if len(allowed) == 0 {
		return filtered
	}
	ctx.QueryArgs().VisitAll(func(k, v []byte) {
		for _, ak := range allowed {
			if bytes.HasPrefix(k, ak) {
				filtered = append(filtered, [2][]byte{
					append([]byte(nil), k...),
					append([]byte(nil), v...),
				})
			}
		}
	})
	sort.Slice(filtered, func(i, j int) bool {
		return bytes.Compare(filtered[i][0], filtered[j][0]) < 0
	})
	return filtered
}

func getFilteredAndSortedKeyHeadersFastHttp(r *fasthttp.RequestHeader, allowed [][]byte) (kvPairs [][2][]byte) {
	var filtered = make([][2][]byte, 0, len(allowed))
	if len(allowed) == 0 {
		return filtered
	}
	r.VisitAll(func(key, value []byte) {
		for _, allowedKey := range allowed {
			if bytes.EqualFold(key, allowedKey) {
				filtered = append(filtered, [2][]byte{
					append([]byte(nil), key...),
					append([]byte(nil), value...),
				})
				break
			}
		}
	})
	sort.Slice(filtered, func(i, j int) bool {
		return bytes.Compare(filtered[i][0], filtered[j][0]) < 0
	})
	return filtered
}
