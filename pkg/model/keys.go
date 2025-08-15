package model

import (
	"bytes"
	"encoding/binary"
	"fmt"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/valyala/fasthttp"
	"github.com/zeebo/xxh3"
	"sync"
)

var (
	keyHasherPool = sync.Pool{New: func() any { return xxh3.New() }}
	keyBufPool    = sync.Pool{New: func() interface{} { return new(bytes.Buffer) }}
)

func (e *Entry) MapKey() uint64   { return e.key }
func (e *Entry) ShardKey() uint64 { return e.shard }

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

	hasher := keyHasherPool.Get().(*xxh3.Hasher)
	defer func() {
		hasher.Reset()
		keyHasherPool.Put(hasher)
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

	filteredHeaders, filteredHeadersReleaser := e.getFilteredAndSortedKeyHeadersCtxFasthttp(r)
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
