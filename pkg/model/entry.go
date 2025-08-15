package model

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/ctime"
	"github.com/Borislavv/advanced-cache/pkg/list"
	"github.com/valyala/fasthttp"
	"sync/atomic"
)

var _ CacheItem = (*Entry)(nil)

type CacheItem interface {
	Rule() *config.Rule
	MapKey() uint64
	ShardKey() uint64
	Fingerprint() [16]byte
	IsSameFingerprint(another [16]byte) bool
	SwapPayloads(another *Entry)
	IsSamePayload(another *Entry) bool
	SetPayload(req *fasthttp.Request, resp *fasthttp.Response) *Entry
	Payload() (req *fasthttp.Request, resp *fasthttp.Response, releaser Releaser, err error)
	ShouldBeRefreshed(cfg config.Config) bool
	LruListElement() *list.Element[*Entry]
	SetLruListElement(el *list.Element[*Entry])
	ToBytes() (data []byte, releaseFn func())
	UpdatedAt() int64
	Weight() int64
}

type Entry struct {
	key         uint64   // 64  bit xxh
	shard       uint64   // 64  bit xxh % 2048 (num of shards)
	fingerprint [16]byte // 128 bit xxh
	rule        *atomic.Pointer[config.Rule]
	payload     *atomic.Pointer[[]byte]
	lruListElem *atomic.Pointer[list.Element[*Entry]]
	updatedAt   int64 // atomic: unix nano
}

func (e *Entry) Init() *Entry {
	e.rule = &atomic.Pointer[config.Rule]{}
	e.payload = &atomic.Pointer[[]byte]{}
	e.lruListElem = &atomic.Pointer[list.Element[*Entry]]{}
	atomic.StoreInt64(&e.updatedAt, ctime.UnixNano())
	return e
}

func NewEntry(cfg config.Config, r *fasthttp.RequestCtx) (*Entry, error) {
	rule := MatchRule(cfg, r.Path())
	if rule == nil {
		return nil, cacheRuleNotFoundError
	}

	entry := new(Entry).Init()
	entry.rule.Store(rule)

	filteredQueries, filteredQueriesReleaser := entry.getFilteredAndSortedKeyQueriesFastHttp(r)
	defer filteredQueriesReleaser(filteredQueries)

	filteredHeaders, filteredHeadersReleaser := entry.getFilteredAndSortedKeyHeadersCtxFasthttp(r)
	defer filteredHeadersReleaser(filteredHeaders)

	entry.calculateAndSetUpKeys(filteredQueries, filteredHeaders)

	return entry, nil
}

func NewEntryWithPayload(cfg config.Config, req *fasthttp.Request, resp *fasthttp.Response) (*Entry, error) {
	rule := MatchRule(cfg, req.URI().Path())
	if rule == nil {
		return nil, cacheRuleNotFoundError
	}

	entry := new(Entry).Init()
	entry.rule.Store(rule)

	filteredQueries, filteredQueriesReleaser := entry.parseFilterAndSortQuery(req.URI().QueryString())
	defer filteredQueriesReleaser(filteredQueries)

	filteredHeaders, filteredHeadersReleaser := entry.getFilteredAndSortedKeyHeadersFasthttp(req)
	defer filteredHeadersReleaser(filteredHeaders)

	entry.calculateAndSetUpKeys(filteredQueries, filteredHeaders)

	entry.SetPayload(req, resp)

	return entry, nil
}

func newEntryFromField(
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
