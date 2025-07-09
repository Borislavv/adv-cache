package model

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/list"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/valyala/fasthttp"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"
)

type Revalidator func(ctx context.Context, path []byte, query []byte, queryHeaders [][2][]byte) (status int, headers http.Header, body []byte, err error)

// Entry is the packed request+response payload
type Entry struct {
	key          uint64
	shard        uint64
	rule         *config.Rule
	payload      *atomic.Pointer[[]byte]
	lruListElem  *atomic.Pointer[list.Element[*Entry]]
	revalidator  Revalidator
	willUpdateAt int64
	isCompressed int64
}

func NewEntryFromField(
	key uint64,
	shard uint64,
	payload []byte,
	rule *config.Rule,
	revalidator Revalidator,
	willUpdateAt int64,
) *Entry {
	e := &Entry{
		key:          key,
		shard:        shard,
		payload:      &atomic.Pointer[[]byte]{},
		rule:         rule,
		lruListElem:  &atomic.Pointer[list.Element[*Entry]]{},
		revalidator:  revalidator,
		willUpdateAt: willUpdateAt,
	}
	e.payload.Store(&payload)
	return e
}

//var (
//	bufPool        = &sync.Pool{New: func() any { return new(bytes.Buffer) }}
//	GzipBufferPool = &sync.Pool{New: func() any { return new(bytes.Buffer) }}
//	GzipWriterPool = &sync.Pool{New: func() any {
//		w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
//		return w
//	}}
//	RuleNotFoundError = errors.New("rule not found")
//)
//
//const gzipThreshold = 1024

func (e *Entry) MapKey() uint64   { return e.key }
func (e *Entry) ShardKey() uint64 { return e.shard }

// NewEntry accepts path, query and request headers as bytes slices.
func NewEntry(cfg *config.Cache, r *fasthttp.RequestCtx) (*Entry, error) {
	rule := MatchRule(cfg, r.Path())
	if rule == nil {
		return nil, RuleNotFoundError
	}

	entry := &Entry{
		rule:        rule,
		lruListElem: &atomic.Pointer[list.Element[*Entry]]{},
		payload:     &atomic.Pointer[[]byte]{},
	}

	return entry.calculateAndSetUpKeys(
		entry.getFilteredAndSortedKeyQueries(r),
		entry.getFilteredAndSortedKeyHeaders(r),
	), nil
}

func NewEntryManual(cfg *config.Cache, path, query []byte, headers [][2][]byte, revalidator Revalidator) (*Entry, error) {
	entry := &Entry{
		rule:        MatchRule(cfg, path),
		lruListElem: &atomic.Pointer[list.Element[*Entry]]{},
		payload:     &atomic.Pointer[[]byte]{},
		revalidator: revalidator,
	}
	if entry.rule == nil {
		return nil, RuleNotFoundError
	}

	// initial request payload (response payload will be filled later)
	entry.SetPayload(path, query, headers, nil, nil, 0)

	entry.calculateAndSetUpKeys(parseQuery(query), headers)

	return entry, nil
}

func (e *Entry) calculateAndSetUpKeys(filteredQueries, filteredHeaders [][2][]byte) *Entry {
	queryLen := 0
	for _, pair := range filteredQueries {
		queryLen += len(pair[0]) + len(pair[1])
	}
	headersLen := 0
	for _, pair := range filteredHeaders {
		headersLen += len(pair[0]) + len(pair[1])
	}

	buf := bufPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		bufPool.Put(buf)
	}()
	buf.Grow(queryLen + headersLen)

	for _, pair := range filteredQueries {
		buf.Write(pair[0])
		buf.Write(pair[1])
	}
	for _, pair := range filteredHeaders {
		buf.Write(pair[0])
		buf.Write(pair[1])
	}

	e.key = hash(buf)
	e.shard = sharded.MapShardKey(e.key)

	return e
}

func (e *Entry) SetRevalidator(revalidator Revalidator) {
	e.revalidator = revalidator
}

// SetPayload packs and gzip-compresses the entire payload: Path, Query, Status, Headers, Body.
// SetPayload packs and gzip-compresses the entire payload: Path, Query, QueryHeaders, Status, ResponseHeaders, Body.
func (e *Entry) SetPayload(
	path, query []byte,
	queryHeaders [][2][]byte,
	body []byte,
	headers http.Header,
	status int,
) {
	numQueryHeaders := len(queryHeaders)
	numResponseHeaders := len(headers)

	estSize := 128 + len(path) + len(query) + len(body)
	for _, kv := range queryHeaders {
		estSize += len(kv[0]) + len(kv[1])
	}
	for key, vv := range headers {
		for _, v := range vv {
			estSize += len(key) + len(v)
		}
	}

	buf := make([]byte, 0, estSize)

	// --- Path
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(path)))
	buf = append(buf, path...)

	// --- Query
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(query)))
	buf = append(buf, query...)

	// --- QueryHeaders
	buf = binary.LittleEndian.AppendUint32(buf, uint32(numQueryHeaders))
	for _, kv := range queryHeaders {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(kv[0])))
		buf = append(buf, kv[0]...)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(kv[1])))
		buf = append(buf, kv[1]...)
	}

	// --- StatusCode
	buf = binary.LittleEndian.AppendUint32(buf, uint32(status))

	// --- Response Headers
	buf = binary.LittleEndian.AppendUint32(buf, uint32(numResponseHeaders))
	for k, vs := range headers {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(k)))
		buf = append(buf, []byte(k)...)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(vs)))
		for _, v := range vs {
			buf = binary.LittleEndian.AppendUint32(buf, uint32(len(v)))
			buf = append(buf, []byte(v)...)
		}
	}

	// --- Body
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(body)))
	buf = append(buf, body...)

	if e.rule.Gzip.Enabled && len(buf) >= e.rule.Gzip.Threshold {
		// --- Compress entire payload
		gzipper := GzipWriterPool.Get().(*gzip.Writer)
		defer GzipWriterPool.Put(gzipper)

		bufIn := GzipBufferPool.Get().(*bytes.Buffer)
		defer GzipBufferPool.Put(bufIn)
		bufIn.Reset()
		gzipper.Reset(bufIn)

		_, err := gzipper.Write(buf)
		closeErr := gzipper.Close()
		if err == nil && closeErr == nil {
			tmpBuf := make([]byte, bufIn.Len())
			copy(tmpBuf, bufIn.Bytes())
			e.payload.Store(&tmpBuf)
		} else {
			e.payload.Store(&buf)
		}

		atomic.StoreInt64(&e.isCompressed, 1)
	} else {
		e.payload.Store(&buf)
	}
}

// Payload decompresses the entire payload and unpacks it into fields.
func (e *Entry) Payload() (
	path []byte,
	query []byte,
	queryHeaders [][2][]byte,
	status int,
	responseHeaders http.Header,
	body []byte,
	err error,
) {
	payloadPtr := e.payload.Load()
	if payloadPtr == nil {
		return nil, nil, nil, 0, nil, nil, fmt.Errorf("payload is nil")
	}

	var rawPayload []byte

	if atomic.LoadInt64(&e.isCompressed) == 1 {
		gr, err := gzip.NewReader(bytes.NewReader(*payloadPtr))
		if err != nil {
			return nil, nil, nil, 0, nil, nil, fmt.Errorf("gzip reader failed: %w", err)
		}
		defer gr.Close()

		rawPayload, err = io.ReadAll(gr)
		if err != nil {
			return nil, nil, nil, 0, nil, nil, fmt.Errorf("gzip read failed: %w", err)
		}
	} else {
		rawPayload = *payloadPtr
	}

	offset := 0

	// --- Path
	pathLen := binary.LittleEndian.Uint32(rawPayload[offset:])
	offset += 4
	path = rawPayload[offset : offset+int(pathLen)]
	offset += int(pathLen)

	// --- Query
	queryLen := binary.LittleEndian.Uint32(rawPayload[offset:])
	offset += 4
	query = rawPayload[offset : offset+int(queryLen)]
	offset += int(queryLen)

	// --- QueryHeaders
	numQueryHeaders := binary.LittleEndian.Uint32(rawPayload[offset:])
	offset += 4
	queryHeaders = make([][2][]byte, numQueryHeaders)
	for i := 0; i < int(numQueryHeaders); i++ {
		keyLen := binary.LittleEndian.Uint32(rawPayload[offset:])
		offset += 4
		k := rawPayload[offset : offset+int(keyLen)]
		offset += int(keyLen)

		valueLen := binary.LittleEndian.Uint32(rawPayload[offset:])
		offset += 4
		v := rawPayload[offset : offset+int(valueLen)]
		offset += int(valueLen)

		queryHeaders[i] = [2][]byte{k, v}
	}

	// --- Status
	status = int(binary.LittleEndian.Uint32(rawPayload[offset:]))
	offset += 4

	// --- Response Headers
	numHeaders := binary.LittleEndian.Uint32(rawPayload[offset:])
	offset += 4
	responseHeaders = make(http.Header)
	for i := 0; i < int(numHeaders); i++ {
		keyLen := binary.LittleEndian.Uint32(rawPayload[offset:])
		offset += 4
		keyStr := string(rawPayload[offset : offset+int(keyLen)])
		offset += int(keyLen)

		numVals := binary.LittleEndian.Uint32(rawPayload[offset:])
		offset += 4
		for v := 0; v < int(numVals); v++ {
			valueLen := binary.LittleEndian.Uint32(rawPayload[offset:])
			offset += 4
			valStr := string(rawPayload[offset : offset+int(valueLen)])
			offset += int(valueLen)
			responseHeaders.Add(keyStr, valStr)
		}
	}

	// --- Body
	bodyLen := binary.LittleEndian.Uint32(rawPayload[offset:])
	offset += 4
	body = rawPayload[offset : offset+int(bodyLen)]

	return
}

func (e *Entry) Rule() *config.Rule {
	return e.rule
}

func (e *Entry) PayloadBytes() []byte {
	return *e.payload.Load()
}

func (e *Entry) Weight() int64 {
	return int64(unsafe.Sizeof(*e)) + int64(cap(*e.payload.Load()))
}

func (e *Entry) IsCompressed() bool {
	return atomic.LoadInt64(&e.isCompressed) == 1
}

func (e *Entry) WillUpdateAt() int64 {
	return atomic.LoadInt64(&e.willUpdateAt)
}

func parseQuery(b []byte) [][2][]byte {
	b = bytes.TrimLeft(b, "?")
	kvPairs := bytes.Split(b, []byte("&"))

	var query [][2][]byte
	for _, kvPair := range kvPairs {
		kv := bytes.Split(kvPair, []byte("="))
		if len(kv) == 2 {
			query = append(query, [2][]byte{kv[0], kv[1]})
		}
	}
	return query
}

func (e *Entry) getFilteredAndSortedKeyQueries(r *fasthttp.RequestCtx) (kvPairs [][2][]byte) {
	var filtered = make([][2][]byte, 0, len(e.rule.CacheKey.QueryBytes))
	if cap(filtered) == 0 {
		return filtered
	}
	r.QueryArgs().All()(func(key, value []byte) bool {
		for _, ak := range e.rule.CacheKey.QueryBytes {
			if bytes.HasPrefix(key, ak) {
				filtered = append(filtered, [2][]byte{key, value})
				break
			}
		}
		return true
	})
	sort.Slice(filtered, func(i, j int) bool {
		return bytes.Compare(filtered[i][0], filtered[j][0]) < 0
	})
	return filtered
}

func (e *Entry) getFilteredAndSortedKeyHeaders(r *fasthttp.RequestCtx) (kvPairs [][2][]byte) {
	var filtered = make([][2][]byte, 0, len(e.rule.CacheKey.HeadersBytes))
	if cap(filtered) == 0 {
		return filtered
	}
	r.Request.Header.All()(func(key, value []byte) bool {
		for _, allowedKey := range e.rule.CacheKey.HeadersBytes {
			if bytes.EqualFold(key, allowedKey) {
				filtered = append(filtered, [2][]byte{key, value})
				break
			}
		}
		return true
	})
	sort.Slice(filtered, func(i, j int) bool {
		return bytes.Compare(filtered[i][0], filtered[j][0]) < 0
	})
	return filtered
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
func (e *Entry) ShouldBeRefreshed(cfg *config.Cache) bool {
	if e == nil {
		return false
	}

	now := time.Now().UnixNano()
	remaining := atomic.LoadInt64(&e.willUpdateAt) - now

	if remaining < 0 {
		remaining = 0 // it's time already
	}

	interval := e.rule.TTL.Nanoseconds()
	if interval == 0 {
		interval = cfg.Cache.Refresh.TTL.Nanoseconds()
	}

	beta := e.rule.Beta
	if beta == 0 {
		beta = cfg.Cache.Refresh.Beta
	}

	// Probability: higher as we approach ShouldRevalidatedAt
	prob := 1 - math.Exp(-beta*(1.0-(float64(remaining)/float64(interval))))

	return rand.Float64() < prob
}

// Revalidate calls the revalidator closure to fetch fresh data and updates the timestamp.
func (e *Entry) Revalidate(ctx context.Context) error {
	path, query, headers, _, _, _, err := e.Payload()
	if err != nil {
		return err
	}

	statusCode, respHeaders, body, err := e.revalidator(ctx, path, query, headers)
	if err != nil {
		return err
	}

	e.SetPayload(path, query, headers, body, respHeaders, statusCode)

	if statusCode == http.StatusOK {
		atomic.StoreInt64(&e.willUpdateAt, time.Now().UnixNano()+e.rule.TTL.Nanoseconds())
	} else {
		atomic.StoreInt64(&e.willUpdateAt, time.Now().UnixNano()+e.rule.ErrorTTL.Nanoseconds())
	}

	return nil
}
