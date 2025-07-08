package model

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/list"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/valyala/fasthttp"
	"io"
	"net/http"
	"sort"
	"sync/atomic"
	"unsafe"
)

// Entry is the packed request+response payload
type Entry struct {
	key           uint64
	shard         uint64
	payload       []byte
	rule          *config.Rule
	lruListElem   *atomic.Pointer[list.Element[*Entry]]
	revalidator   func(ctx context.Context) (payload []byte, err error)
	revalidatedAt int64
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
	entry := &Entry{rule: matchRule(cfg, r.Path())}
	if entry.rule == nil {
		return nil, RuleNotFoundError
	}
	return entry.calculateAndSetUpKeys(
		entry.getFilteredAndSortedKeyQueries(r),
		entry.getFilteredAndSortedKeyHeaders(r),
	), nil
}

func NewEntryManual(cfg *config.Cache, path, query []byte, headers [][2][]byte) (*Entry, error) {
	entry := &Entry{rule: matchRule(cfg, path)}
	if entry.rule == nil {
		return nil, RuleNotFoundError
	}
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

// SetPayload packs and gzip-compresses the entire payload: Path, Query, Status, Headers, Body.
func (e *Entry) SetPayload(path, query, body []byte, headers http.Header, status int) {
	numHeaders := len(headers)
	estSize := 64 + len(path) + len(query) + len(body)
	for key, vv := range headers {
		for _, value := range vv {
			estSize += len(key) + len(value)
		}
	}

	buf := make([]byte, 0, estSize)

	// --- Path
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(path)))
	buf = append(buf, path...)

	// --- Query
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(query)))
	buf = append(buf, query...)

	// --- StatusCode
	buf = binary.LittleEndian.AppendUint32(buf, uint32(status))

	// --- Headers
	buf = binary.LittleEndian.AppendUint32(buf, uint32(numHeaders))
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

	// Always compress entire payload
	gzipper := GzipWriterPool.Get().(*gzip.Writer)
	defer GzipWriterPool.Put(gzipper)

	bufIn := GzipBufferPool.Get().(*bytes.Buffer)
	defer GzipBufferPool.Put(bufIn)
	bufIn.Reset()
	gzipper.Reset(bufIn)

	_, err := gzipper.Write(buf)
	closeErr := gzipper.Close()
	if err == nil && closeErr == nil {
		e.payload = append([]byte(nil), bufIn.Bytes()...)
	} else {
		e.payload = buf // fallback uncompressed (should not happen)
	}
}

// Payload decompresses the entire payload and unpacks it into fields.
func (e *Entry) Payload() (
	path []byte,
	query []byte,
	status int,
	headers http.Header,
	body []byte,
) {
	gr, err := gzip.NewReader(bytes.NewReader(e.payload))
	if err != nil {
		// fallback: raw payload if broken
		return nil, nil, 0, nil, e.payload
	}
	defer gr.Close()

	rawPayload, err := io.ReadAll(gr)
	if err != nil {
		return nil, nil, 0, nil, e.payload
	}

	offset := 0

	// --- Path
	plen := binary.LittleEndian.Uint32(rawPayload[offset:])
	offset += 4
	path = rawPayload[offset : offset+int(plen)]
	offset += int(plen)

	// --- Query
	qlen := binary.LittleEndian.Uint32(rawPayload[offset:])
	offset += 4
	query = rawPayload[offset : offset+int(qlen)]
	offset += int(qlen)

	// --- Status
	status = int(binary.LittleEndian.Uint32(rawPayload[offset:]))
	offset += 4

	// --- Headers
	numHeaders := binary.LittleEndian.Uint32(rawPayload[offset:])
	offset += 4
	headers = make(http.Header)
	for i := 0; i < int(numHeaders); i++ {
		klen := binary.LittleEndian.Uint32(rawPayload[offset:])
		offset += 4
		keyStr := string(rawPayload[offset : offset+int(klen)])
		offset += int(klen)

		numVals := binary.LittleEndian.Uint32(rawPayload[offset:])
		offset += 4
		for v := 0; v < int(numVals); v++ {
			vlen := binary.LittleEndian.Uint32(rawPayload[offset:])
			offset += 4
			valStr := string(rawPayload[offset : offset+int(vlen)])
			offset += int(vlen)
			headers.Add(keyStr, valStr)
		}
	}

	// --- Body
	blen := binary.LittleEndian.Uint32(rawPayload[offset:])
	offset += 4
	body = rawPayload[offset : offset+int(blen)]

	return
}

func (e *Entry) Weight() int64 {
	return int64(unsafe.Sizeof(*e)) + int64(cap(e.payload))
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
	r.QueryArgs().VisitAll(func(key, value []byte) {
		for _, ak := range e.rule.CacheKey.QueryBytes {
			if bytes.HasPrefix(key, ak) {
				filtered = append(filtered, [2][]byte{key, value})
				break
			}
		}
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
	r.Request.Header.VisitAll(func(key, value []byte) {
		for _, allowedKey := range e.rule.CacheKey.HeadersBytes {
			if bytes.EqualFold(key, allowedKey) {
				filtered = append(filtered, [2][]byte{key, value})
				break
			}
		}
	})
	sort.Slice(filtered, func(i, j int) bool {
		return bytes.Compare(filtered[i][0], filtered[j][0]) < 0
	})
	return filtered
}
