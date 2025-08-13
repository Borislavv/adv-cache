package model

import (
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	bytes2 "github.com/Borislavv/advanced-cache/pkg/bytes"
	"github.com/Borislavv/advanced-cache/pkg/ctime"
	"github.com/rs/zerolog/log"
	"math"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/list"
	"github.com/Borislavv/advanced-cache/pkg/sort"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/valyala/fasthttp"
	"github.com/zeebo/xxh3"
)

var (
	bufPool           = &sync.Pool{New: func() any { return new(bytes.Buffer) }}
	hasherPool        = &sync.Pool{New: func() any { return xxh3.New() }}
	ruleNotFoundError = errors.New("rule not found")
)

// Entry is the packed request+response payload
type Entry struct {
	key         uint64   // 64  bit xxh
	shard       uint64   // 64  bit xxh % NumOfShards
	fingerprint [16]byte // 128 bit xxh
	rule        *atomic.Pointer[config.Rule]
	payload     *atomic.Pointer[[]byte]
	lruListElem *atomic.Pointer[list.Element[*Entry]]
	updatedAt   int64 // atomic: unix nano (last update was at)
}

func (e *Entry) Init() *Entry {
	e.rule = &atomic.Pointer[config.Rule]{}
	e.payload = &atomic.Pointer[[]byte]{}
	e.lruListElem = &atomic.Pointer[list.Element[*Entry]]{}
	atomic.StoreInt64(&e.updatedAt, ctime.UnixNano())
	return e
}

// NewEntryNetHttp accepts path, query and request headers as bytes slices.
func NewEntryNetHttp(rule *config.Rule, r *http.Request) *Entry {
	entry := new(Entry).Init()
	entry.rule.Store(rule)

	filteredQueries, filteredQueriesReleaser := entry.GetFilteredAndSortedKeyQueriesNetHttp(r)
	defer filteredQueriesReleaser(filteredQueries)

	filteredHeaders, filteredHeadersReleaser := entry.GetFilteredAndSortedKeyHeadersNetHttp(r)
	defer filteredHeadersReleaser(filteredHeaders)

	entry.calculateAndSetUpKeys(filteredQueries, filteredHeaders)

	return entry
}

// NewEntryFastHttp accepts path, query and request headers as bytes slices.
func NewEntryFastHttp(cfg config.Config, r *fasthttp.RequestCtx) (*Entry, error) {
	rule := MatchRule(cfg, r.Path())
	if rule == nil {
		return nil, ruleNotFoundError
	}

	entry := new(Entry).Init()
	entry.rule.Store(rule)

	filteredQueries, filteredQueriesReleaser := entry.getFilteredAndSortedKeyQueriesFastHttp(r)
	defer filteredQueriesReleaser(filteredQueries)

	filteredHeaders, filteredHeadersReleaser := entry.getFilteredAndSortedKeyHeadersFastHttp(r)
	defer filteredHeadersReleaser(filteredHeaders)

	entry.calculateAndSetUpKeys(filteredQueries, filteredHeaders)

	return entry, nil
}

func NewMockEntry(cfg config.Config, req *fasthttp.Request, resp *fasthttp.Response) (*Entry, error) {
	path := req.URI().Path()
	query := req.URI().QueryString()

	rule := MatchRule(cfg, path)
	if rule == nil {
		return nil, ruleNotFoundError
	}

	entry := new(Entry).Init()
	entry.rule.Store(rule)

	filteredQueries, filteredQueriesReleaser := entry.parseFilterAndSortQuery(query) // here, we are referring to the same query buffer which used in payload which have been mentioned before
	defer filteredQueriesReleaser(filteredQueries)                                   // this is really reduce memory usage and GC pressure

	filteredHeaders, filteredHeadersReleaser := entry.getFilteredAndSortedKeyHeadersFasthttp(req)
	defer filteredHeadersReleaser(filteredHeaders)

	entry.calculateAndSetUpKeys(filteredQueries, filteredHeaders)

	entry.SetPayload(req, resp)

	return entry, nil
}

func NewEntryFromField(
	key uint64,
	shard uint64,
	fingerprint [16]byte,
	payload []byte,
	rule *config.Rule,
	updatedAt int64,
) *Entry {
	entry := new(Entry).Init()
	entry.key = key
	entry.shard = shard
	entry.fingerprint = fingerprint
	entry.rule.Store(rule)
	entry.payload.Store(&payload)
	entry.updatedAt = updatedAt
	return entry
}

func (e *Entry) MapKey() uint64     { return e.key }
func (e *Entry) ShardKey() uint64   { return e.shard }
func (e *Entry) Rule() *config.Rule { return e.rule.Load() }

var keyBufPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

func (e *Entry) calculateAndSetUpKeys(filteredQueries, filteredHeaders *[][2][]byte) *Entry {
	l := 0
	for _, pair := range *filteredQueries {
		l += len(pair[0]) + len(pair[1])
	}
	for _, pair := range *filteredHeaders {
		l += len(pair[0]) + len(pair[1])
	}

	buf := keyBufPool.Get().(*bytes.Buffer)
	buf.Grow(l)
	defer func() {
		buf.Reset()
		keyBufPool.Put(buf)
	}()

	for _, pair := range *filteredQueries {
		buf.Write(pair[0])
		buf.Write(pair[1])
	}
	for _, pair := range *filteredHeaders {
		buf.Write(pair[0])
		buf.Write(pair[1])
	}

	hasher := hasherPool.Get().(*xxh3.Hasher)
	defer func() {
		hasher.Reset()
		hasherPool.Put(hasher)
	}()

	// calculate key hash
	if _, err := hasher.Write(buf.Bytes()); err != nil {
		panic(err)
	}
	e.key = hasher.Sum64()

	// calculate fingerprint hash
	fp := hasher.Sum128()
	var fingerprint [16]byte
	binary.LittleEndian.PutUint64(fingerprint[0:8], fp.Lo)
	binary.LittleEndian.PutUint64(fingerprint[8:16], fp.Hi)
	e.fingerprint = fingerprint

	// calculate shard index
	e.shard = sharded.MapShardKey(e.key)

	return e
}

func (e *Entry) DumpBuffer(r *fasthttp.RequestCtx) {
	filteredQueries, filteredQueriesReleaser := e.getFilteredAndSortedKeyQueriesFastHttp(r)
	defer filteredQueriesReleaser(filteredQueries)

	filteredHeaders, filteredHeadersReleaser := e.getFilteredAndSortedKeyHeadersFastHttp(r)
	defer filteredHeadersReleaser(filteredHeaders)

	l := 0
	for _, pair := range *filteredQueries {
		l += len(pair[0]) + len(pair[1])
	}
	for _, pair := range *filteredHeaders {
		l += len(pair[0]) + len(pair[1])
	}

	buf := keyBufPool.Get().(*bytes.Buffer)
	buf.Grow(l)
	defer func() {
		buf.Reset()
		keyBufPool.Put(buf)
	}()

	for _, pair := range *filteredQueries {
		buf.Write(pair[0])
		buf.Write(pair[1])
	}
	for _, pair := range *filteredHeaders {
		buf.Write(pair[0])
		buf.Write(pair[1])
	}

	fmt.Println("DumpBuffer: ", buf.String())
}

func (e *Entry) Fingerprint() [16]byte {
	return e.fingerprint
}

func (e *Entry) IsSamePayload(another *Entry) bool {
	return e.isPayloadsAreEquals(e.PayloadBytes(), another.PayloadBytes())
}

func (e *Entry) IsSameFingerprint(another [16]byte) bool {
	return subtle.ConstantTimeCompare(e.fingerprint[:], another[:]) == 1
}

func (e *Entry) IsSameEntry(another *Entry) bool {
	return subtle.ConstantTimeCompare(e.fingerprint[:], another.fingerprint[:]) == 1 &&
		e.isPayloadsAreEquals(e.PayloadBytes(), another.PayloadBytes())
}

func (e *Entry) SwapPayloads(another *Entry) {
	another.payload.Store(e.payload.Swap(another.payload.Load()))
	e.touch()
}

func (e *Entry) touch() {
	atomic.StoreInt64(&e.updatedAt, ctime.UnixNano())
}

func (e *Entry) isPayloadsAreEquals(a, b []byte) bool {
	return bytes2.IsBytesAreEquals(a, b)
}

// SetPayload packs and gzip-compresses the entire payload: Path, Query, QueryHeaders, StatusCode, ResponseHeaders, Body.
func (e *Entry) SetPayload(req *fasthttp.Request, resp *fasthttp.Response) *Entry {
	path := req.URI().Path()
	query := req.URI().QueryString()
	body := resp.Body()

	// === 1) Calculate total size ===
	total := 0
	total += 4 + len(path)
	total += 4 + len(query)
	total += 4
	req.Header.All()(func(k []byte, v []byte) bool {
		total += 4 + len(k) + 4 + len(v)
		return true
	})
	total += 4
	total += 4
	resp.Header.All()(func(k []byte, v []byte) bool {
		total += 4 + len(k) + 4 + 4 + len(v)
		return true
	})
	total += 4 + len(body)

	// === 2) Allocate ===
	payloadBuf := make([]byte, 0, total)
	offset := 0

	var scratch [4]byte

	// === 3) Write ===

	// Path
	binary.LittleEndian.PutUint32(scratch[:], uint32(len(path)))
	payloadBuf = append(payloadBuf, scratch[:]...)
	payloadBuf = append(payloadBuf, path...)
	offset += len(path)

	// Query
	binary.LittleEndian.PutUint32(scratch[:], uint32(len(query)))
	payloadBuf = append(payloadBuf, scratch[:]...)
	payloadBuf = append(payloadBuf, query...)
	offset += len(query)

	// QueryHeaders
	binary.LittleEndian.PutUint32(scratch[:], uint32(req.Header.Len()))
	payloadBuf = append(payloadBuf, scratch[:]...)
	req.Header.All()(func(k []byte, v []byte) bool {
		binary.LittleEndian.PutUint32(scratch[:], uint32(len(k)))
		payloadBuf = append(payloadBuf, scratch[:]...)
		payloadBuf = append(payloadBuf, k...)
		offset += len(k)

		binary.LittleEndian.PutUint32(scratch[:], uint32(len(v)))
		payloadBuf = append(payloadBuf, scratch[:]...)
		payloadBuf = append(payloadBuf, v...)
		offset += len(v)
		return true
	})

	// StatusCode
	binary.LittleEndian.PutUint32(scratch[:], uint32(resp.StatusCode()))
	payloadBuf = append(payloadBuf, scratch[:]...)

	// ResponseHeaders
	binary.LittleEndian.PutUint32(scratch[:], uint32(resp.Header.Len()))
	payloadBuf = append(payloadBuf, scratch[:]...)
	resp.Header.All()(func(k []byte, v []byte) bool {
		binary.LittleEndian.PutUint32(scratch[:], uint32(len(k)))
		payloadBuf = append(payloadBuf, scratch[:]...)
		payloadBuf = append(payloadBuf, k...)
		offset += len(k)

		binary.LittleEndian.PutUint32(scratch[:], uint32(1))
		payloadBuf = append(payloadBuf, scratch[:]...)
		binary.LittleEndian.PutUint32(scratch[:], uint32(len(v)))
		payloadBuf = append(payloadBuf, scratch[:]...)
		payloadBuf = append(payloadBuf, v...)
		offset += len(v)
		return true
	})

	// Body
	binary.LittleEndian.PutUint32(scratch[:], uint32(len(body)))
	payloadBuf = append(payloadBuf, scratch[:]...)
	payloadBuf = append(payloadBuf, body...)
	offset += len(body)

	// === 5) Store raw ===
	payloadBuf = payloadBuf[:]
	e.payload.Store(&payloadBuf)

	// Tell everyone that entry already updated
	e.touch()

	return e
}

var ErrPayloadIsEmpty = fmt.Errorf("payload is empty")

// Payload decompresses the entire payload and unpacks it into fields.
func (e *Entry) Payload() (req *fasthttp.Request, resp *fasthttp.Response, releaser func(*fasthttp.Request, *fasthttp.Response), err error) {
	payload := e.PayloadBytes()
	if len(payload) == 0 {
		return nil, nil, emptyReleaser, ErrPayloadIsEmpty
	}

	req = fasthttp.AcquireRequest()
	resp = fasthttp.AcquireResponse()

	offset := 0

	// --- Path
	pathLen := binary.LittleEndian.Uint32(payload[offset:])
	offset += 4
	req.URI().SetPathBytes(payload[offset : offset+int(pathLen)])
	offset += int(pathLen)

	// --- Query
	queryLen := binary.LittleEndian.Uint32(payload[offset:])
	offset += 4
	req.URI().SetQueryStringBytes(payload[offset : offset+int(queryLen)])
	offset += int(queryLen)

	// --- QueryHeaders
	numQueryHeaders := binary.LittleEndian.Uint32(payload[offset:])
	offset += 4
	for i := 0; i < int(numQueryHeaders); i++ {
		keyLen := binary.LittleEndian.Uint32(payload[offset:])
		offset += 4
		k := payload[offset : offset+int(keyLen)]
		offset += int(keyLen)

		valueLen := binary.LittleEndian.Uint32(payload[offset:])
		offset += 4
		v := payload[offset : offset+int(valueLen)]
		offset += int(valueLen)

		req.Header.AddBytesKV(k, v)
	}

	// --- StatusCode
	resp.Header.SetStatusCode(int(binary.LittleEndian.Uint32(payload[offset:])))
	offset += 4

	// --- Response Headers
	numHeaders := binary.LittleEndian.Uint32(payload[offset:])
	offset += 4
	for i := 0; i < int(numHeaders); i++ {
		keyLen := binary.LittleEndian.Uint32(payload[offset:])
		offset += 4
		key := payload[offset : offset+int(keyLen)]
		offset += int(keyLen)

		numVals := binary.LittleEndian.Uint32(payload[offset:])
		offset += 4
		for v := 0; v < int(numVals); v++ {
			valueLen := binary.LittleEndian.Uint32(payload[offset:])
			offset += 4
			val := payload[offset : offset+int(valueLen)]
			offset += int(valueLen)

			resp.Header.AddBytesKV(key, val)
		}
	}

	// --- Body
	offset += 4
	resp.SetBodyRaw(payload[offset:])

	releaser = func(request *fasthttp.Request, response *fasthttp.Response) {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
	}

	return
}

// TODO test with already wrapped req and resp, if it will not good enough then uncomment and change back
//func (e *Entry) Payload() (
//	path []byte,
//	query []byte,
//	queryHeaders *[][2][]byte,
//	responseHeaders *[][2][]byte,
//	body []byte,
//	status int,
//	releaseFn func(q, h *[][2][]byte),
//	err error,
//) {
//	payload := e.PayloadBytes()
//	if len(payload) == 0 {
//		return nil, nil, nil, nil, nil, 0, emptyReleaser, ErrPayloadIsEmpty
//	}
//
//	offset := 0
//
//	// --- Path
//	pathLen := binary.LittleEndian.Uint32(payload[offset:])
//	offset += 4
//	path = payload[offset : offset+int(pathLen)]
//	offset += int(pathLen)
//
//	// --- Query
//	queryLen := binary.LittleEndian.Uint32(payload[offset:])
//	offset += 4
//	query = payload[offset : offset+int(queryLen)]
//	offset += int(queryLen)
//
//	// --- QueryHeaders
//	numQueryHeaders := binary.LittleEndian.Uint32(payload[offset:])
//	offset += 4
//	queryHeaders = pools.KeyValueSlicePool.Get().(*[][2][]byte)
//	for i := 0; i < int(numQueryHeaders); i++ {
//		keyLen := binary.LittleEndian.Uint32(payload[offset:])
//		offset += 4
//		k := payload[offset : offset+int(keyLen)]
//		offset += int(keyLen)
//
//		valueLen := binary.LittleEndian.Uint32(payload[offset:])
//		offset += 4
//		v := payload[offset : offset+int(valueLen)]
//		offset += int(valueLen)
//
//		*queryHeaders = append(*queryHeaders, [2][]byte{k, v})
//	}
//
//	// --- StatusCode
//	status = int(binary.LittleEndian.Uint32(payload[offset:]))
//	offset += 4
//
//	// --- Response Headers
//	numHeaders := binary.LittleEndian.Uint32(payload[offset:])
//	offset += 4
//	responseHeaders = pools.KeyValueSlicePool.Get().(*[][2][]byte)
//	for i := 0; i < int(numHeaders); i++ {
//		keyLen := binary.LittleEndian.Uint32(payload[offset:])
//		offset += 4
//		key := payload[offset : offset+int(keyLen)]
//		offset += int(keyLen)
//
//		numVals := binary.LittleEndian.Uint32(payload[offset:])
//		offset += 4
//		for v := 0; v < int(numVals); v++ {
//			valueLen := binary.LittleEndian.Uint32(payload[offset:])
//			offset += 4
//			val := payload[offset : offset+int(valueLen)]
//			offset += int(valueLen)
//			*responseHeaders = append(*responseHeaders, [2][]byte{key, val})
//		}
//	}
//
//	// --- Body
//	offset += 4
//	body = payload[offset:]
//
//	releaseFn = payloadReleaser
//
//	return
//}

func (e *Entry) PayloadBytes() []byte {
	var payload []byte
	ptr := e.payload.Load()
	if ptr != nil {
		return *ptr
	}
	return payload
}

func (e *Entry) Weight() int64 {
	return int64(unsafe.Sizeof(*e)) + int64(cap(e.PayloadBytes()))
}

func (e *Entry) UpdateAt() int64 {
	return atomic.LoadInt64(&e.updatedAt)
}

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

	return out, queriesReleaser
}

var kvPool = sync.Pool{
	New: func() interface{} {
		sl := make([][2][]byte, 0, 32)
		return &sl
	},
}

var queriesReleaser = func(queries *[][2][]byte) {
	*queries = (*queries)[:0]
	kvPool.Put(queries)
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

	return out, queriesReleaser
}

func (e *Entry) GetFilteredAndSortedKeyQueriesNetHttp(r *http.Request) (kvPairs *[][2][]byte, releaseFn func(*[][2][]byte)) {
	// r.URL.RawQuery - is static immutable string, therefor we can easily refer to it without any allocations.
	return e.parseFilterAndSortQuery(unsafe.Slice(unsafe.StringData(r.URL.RawQuery), len(r.URL.RawQuery)))
}

var hKvPool = sync.Pool{
	New: func() interface{} {
		sl := make([][2][]byte, 0, 32)
		return &sl
	},
}

var headersReleaser = func(headers *[][2][]byte) {
	*headers = (*headers)[:0]
	hKvPool.Put(headers)
}

func (e *Entry) getFilteredAndSortedKeyHeadersFastHttp(r *fasthttp.RequestCtx) (kvPairs *[][2][]byte, releaseFn func(*[][2][]byte)) {
	out := hKvPool.Get().(*[][2][]byte)
	*out = (*out)[:0]
	allowed := e.rule.Load().CacheKey.HeadersMap

	n := 0
	r.Request.Header.All()(func(k, v []byte) bool {
		if _, ok := allowed[unsafe.String(unsafe.SliceData(k), len(k))]; !ok {
			return true
		}

		if n < cap(*out) {
			if n < len(*out) {
				(*out)[n][0] = k
				(*out)[n][1] = v
			} else {
				*out = (*out)[:n+1]
				(*out)[n][0] = k
				(*out)[n][1] = v
			}
		} else {
			*out = append(*out, [2][]byte{k, v})
		}
		n++

		return true
	})

	*out = (*out)[:n]
	if n > 1 {
		sort.KVSlice(*out)
	}

	return out, headersReleaser
}

func (e *Entry) GetFilteredAndSortedKeyHeadersNetHttp(r *http.Request) (kvPairs *[][2][]byte, releaseFn func(*[][2][]byte)) {
	out := hKvPool.Get().(*[][2][]byte)
	*out = (*out)[:0] // reuse

	allowed := e.rule.Load().CacheKey.HeadersMap
	n := 0

	for k, vv := range r.Header {
		// Check if the key is allowed (string compare, avoid conversion)
		if _, ok := allowed[k]; !ok {
			continue
		}

		kb := unsafe.Slice(unsafe.StringData(k), len(k))
		for _, v := range vv {
			vb := unsafe.Slice(unsafe.StringData(v), len(v))

			if n < cap(*out) {
				if n < len(*out) {
					(*out)[n][0] = kb
					(*out)[n][1] = vb
				} else {
					*out = (*out)[:n+1]
					(*out)[n][0] = kb
					(*out)[n][1] = vb
				}
			} else {
				*out = append(*out, [2][]byte{kb, vb})
			}
			n++
		}
	}

	*out = (*out)[:n]
	if n > 1 {
		sort.KVSlice(*out)
	}

	return out, headersReleaser
}

// filteredAndSortedKeyQueriesInPlace - filters an input slice, be careful!
func (e *Entry) filteredAndSortedKeyQueriesInPlace(queries *[][2][]byte) *[][2][]byte {
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

// getFilteredAndSortedKeyHeadersFasthttp - filters an input slice, be careful!
func (e *Entry) getFilteredAndSortedKeyHeadersFasthttp(r *fasthttp.Request) (kvPairs *[][2][]byte, releaseFn func(*[][2][]byte)) {
	out := kvPool.Get().(*[][2][]byte)
	*out = (*out)[:0]

	allowed := e.rule.Load().CacheKey.HeadersMap
	r.Header.All()(func(k, v []byte) bool {
		if _, ok := allowed[unsafe.String(unsafe.SliceData(k), len(k))]; ok {
			*out = append(*out, [2][]byte{k, v})
		}
		return true
	})

	if len(*out) > 1 {
		sort.KVSlice(*out)
	}

	return out, queriesReleaser
}

// SetLruListElement sets the LRU list element pointer.
func (e *Entry) SetLruListElement(el *list.Element[*Entry]) {
	e.lruListElem.Store(el)
}

// LruListElement returns the LRU list element pointer (for LRU cache management).
func (e *Entry) LruListElement() *list.Element[*Entry] {
	return e.lruListElem.Load()
}

// ShouldBeRefreshed implements probabilistic refresh logic ("beta" algorithm).
// Returns true if the entry is stale and, with a probability proportional to its staleness, should be refreshed now.
func (e *Entry) ShouldBeRefreshed(cfg config.Config) bool {
	if e == nil {
		return false
	}

	var (
		ttl         = cfg.Refresh().TTL.Nanoseconds()
		beta        = cfg.Refresh().Beta
		coefficient = cfg.Refresh().Coefficient
	)

	if e.rule.Load().Refresh != nil {
		if !e.rule.Load().Refresh.Enabled {
			return false
		}

		if e.rule.Load().Refresh.TTL.Nanoseconds() > 0 {
			ttl = e.rule.Load().Refresh.TTL.Nanoseconds()
		}
		if e.rule.Load().Refresh.Beta > 0 {
			beta = e.rule.Load().Refresh.Beta
		}
		if e.rule.Load().Refresh.Coefficient > 0 {
			coefficient = e.rule.Load().Refresh.Coefficient
		}
	}

	// время, прошедшее с последнего обновления
	elapsed := ctime.UnixNano() - atomic.LoadInt64(&e.updatedAt)
	minStale := int64(float64(ttl) * coefficient)

	if minStale > elapsed {
		return false
	}

	// нормируем x = elapsed / ttl в [0,1]
	x := float64(elapsed) / float64(ttl)
	if x < 0 {
		x = 0
	} else if x > 1 {
		x = 1
	}

	// вероятность экспоненциального распределения
	prob := 1 - math.Exp(-beta*x)
	return rand.Float64() < prob
}

func (e *Entry) ToBytes() (data []byte, releaseFn func()) {
	var scratch8 [8]byte
	var scratch4 [4]byte

	payload := e.PayloadBytes()
	rulePath := e.Rule().PathBytes

	// Забираем buffer из пула и очищаем
	buf := bufPool.Get().(*bytes.Buffer)
	releaseFn = func() {
		buf.Reset()
		bufPool.Put(buf)
	}

	// === RulePath ===
	binary.LittleEndian.PutUint32(scratch4[:], uint32(len(rulePath)))
	buf.Write(scratch4[:])
	buf.Write(rulePath)

	// === RuleKey ===
	binary.LittleEndian.PutUint64(scratch8[:], e.key)
	buf.Write(scratch8[:])

	// === Shard ===
	binary.LittleEndian.PutUint64(scratch8[:], e.shard)
	buf.Write(scratch8[:])

	// === Fingerprint ===
	buf.Write(e.fingerprint[:])

	// === UpdateAt ===
	binary.LittleEndian.PutUint64(scratch8[:], uint64(e.UpdateAt()))
	buf.Write(scratch8[:])

	// === Payload ===
	binary.LittleEndian.PutUint32(scratch4[:], uint32(len(payload)))
	buf.Write(scratch4[:])
	buf.Write(payload)

	// Возвращаем готовый []byte и release
	return buf.Bytes(), releaseFn
}

func EntryFromBytes(data []byte, cfg config.Config) (*Entry, error) {
	var offset int

	// RulePath
	rulePathLen := binary.LittleEndian.Uint32(data[offset:])
	offset += 4
	rulePath := data[offset : offset+int(rulePathLen)]
	offset += int(rulePathLen)

	rule := MatchRule(cfg, rulePath)
	if rule == nil {
		return nil, fmt.Errorf("rule not found for path: '%s'", string(rulePath))
	}

	// RuleKey
	key := binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	// Shard
	shard := binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	// Fingerprint
	var fp [16]byte
	copy(fp[:], data[offset:offset+16])
	offset += 16

	// UpdateAt
	updatedAt := int64(binary.LittleEndian.Uint64(data[offset:]))
	offset += 8

	// Payload
	payloadLen := binary.LittleEndian.Uint32(data[offset:])
	offset += 4
	payload := data[offset : offset+int(payloadLen)]

	return NewEntryFromField(key, shard, fp, payload, rule, updatedAt), nil
}

func MatchRule(cfg config.Config, path []byte) *config.Rule {
	pathStr := unsafe.String(unsafe.SliceData(path), len(path))
	if rule, ok := cfg.Rule(pathStr); ok {
		return rule
	}
	return nil
}

// SetMapKey is really dangerous - must be used exclusively in tests.
func (e *Entry) SetMapKey(key uint64) *Entry {
	e.key = key
	return e
}

func (e *Entry) DumpPayload() {
	req, resp, releaser, err := e.Payload()
	defer releaser(req, resp)
	if err != nil {
		log.Error().Err(err).Msg("[dump] failed to unpack payload")
		return
	}

	fmt.Printf("\n========== DUMP PAYLOAD ==========\n")
	fmt.Printf("RuleKey:          %d\n", e.key)
	fmt.Printf("Shard:        %d\n", e.shard)
	fmt.Printf("UpdateAt:	 %s\n", time.Unix(0, e.UpdateAt()).Format(time.RFC3339Nano))
	fmt.Printf("----------------------------------\n")

	fmt.Printf("Path:   	   %q\n", string(req.URI().Path()))
	fmt.Printf("Query:  	   %q\n", string(req.URI().QueryString()))
	fmt.Printf("StatusCode: %d\n", resp.StatusCode())

	fmt.Printf("\nQuery Headers:\n")
	if req.Header.Len() == 0 {
		fmt.Println("  (none)")
	} else {
		req.Header.All()(func(k []byte, v []byte) bool {
			fmt.Printf("  - %q : %q\n", k, v)
			return true
		})
	}

	fmt.Printf("\nResponse Headers:\n")
	if resp.Header.Len() == 0 {
		fmt.Println("  (none)")
	} else {
		resp.Header.All()(func(k []byte, v []byte) bool {
			fmt.Printf("  - %q : %q\n", k, v)
			return true
		})
	}

	fmt.Printf("\nBody (%d bytes):\n", len(resp.Body()))
	if len(resp.Body()) > 0 {
		const maxLen = 500
		if len(resp.Body()) > maxLen {
			fmt.Printf("  %q ... [truncated, total %d bytes]\n", resp.Body()[:maxLen], len(resp.Body()))
		} else {
			fmt.Printf("  %q\n", resp.Body())
		}
	}

	fmt.Println("==================================")
}
