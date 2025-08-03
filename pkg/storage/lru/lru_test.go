package lru

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/stretchr/testify/assert"
)

// dummyBackend is a lightweight in‑memory implementation of repository.Backender for tests.
type dummyBackend struct{}

func (d dummyBackend) Fetch(*config.Rule, []byte, []byte, *[][2][]byte) (int, *[][2][]byte, []byte, func(), error) {
	return 200, nil, nil, func() {}, nil
}

func (d dummyBackend) RevalidatorMaker() func(*config.Rule, []byte, []byte, *[][2][]byte) (int, *[][2][]byte, []byte, func(), error) {
	return func(*config.Rule, []byte, []byte, *[][2][]byte) (int, *[][2][]byte, []byte, func(), error) {
		return 200, nil, nil, func() {}, nil
	}
}

// newTestCfg builds a minimal cache configuration for lru tests.
func newTestCfg(capBytes uint) *config.Cache {
	rules := make(map[string]*config.Rule, 4)
	rules["/foo"] = &config.Rule{}
	rules["/bar"] = &config.Rule{}
	rules["/baz"] = &config.Rule{}
	rules["/hey"] = &config.Rule{}
	return &config.Cache{Cache: &config.CacheBox{
		Enabled:     true,
		Storage:     &config.Storage{Type: "malloc", Size: capBytes},
		Eviction:    &config.Eviction{Enabled: true, Threshold: 0.5},
		Preallocate: config.Preallocation{Shards: 1, PerShard: 0},
		Rules:       rules,
	}}
}

func TestLRU_SetGet(t *testing.T) {
	ctx := context.Background()
	cfg := newTestCfg(1 << 20)
	backend := dummyBackend{}
	cache := NewStorage(ctx, cfg, backend)

	entry := mock.GenRndEntry(cfg, backend, []byte("/foo"))
	ok := cache.Set(entry)
	assert.True(t, ok)

	got, found := cache.Get(entry)
	assert.True(t, found)
	assert.Equal(t, entry.Fingerprint(), got.Fingerprint())
}

func TestLRU_EvictOldest(t *testing.T) {
	ctx := context.Background()
	// Very small capacity to trigger eviction
	cfg := newTestCfg(256)
	backend := dummyBackend{}
	cache := NewStorage(ctx, cfg, backend)

	shortPayload := []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	for i := 0; i < 20; i++ {
		e := mock.GenRndEntry(cfg, backend, []byte("/"+time.Now().String()))
		e.SetPayload(shortPayload, shortPayload, &[][2][]byte{}, &[][2][]byte{}, shortPayload, 200)
		cache.Set(e)
	}

	assert.True(t, cache.ShouldEvict())
}

func TestLRU_GetEmpty(t *testing.T) {
	ctx := context.Background()
	cfg := newTestCfg(1 << 20)
	backend := dummyBackend{}
	cache := NewStorage(ctx, cfg, backend)

	_, found := cache.Get(mock.GenRndEntry(cfg, backend, []byte("/bar")))
	assert.False(t, found)
}

func TestLRU_UpdateExisting(t *testing.T) {
	ctx := context.Background()
	cfg := newTestCfg(1 << 20)
	backend := dummyBackend{}
	cache := NewStorage(ctx, cfg, backend)

	e1 := mock.GenRndEntry(cfg, backend, []byte("/bar"))
	cache.Set(e1)

	// mutate payload
	e2 := mock.GenRndEntry(cfg, backend, []byte("/bar"))
	e2.SetPayload([]byte("/baz"), []byte("new_query"), &[][2][]byte{}, &[][2][]byte{}, []byte("new_body"), 200)
	cache.Set(e2)

	got, found := cache.Get(e1)
	assert.True(t, found)

	path, query, qh, rh, body, _, releaser, err := got.Payload()
	assert.NoError(t, err)
	defer releaser(qh, rh)
	assert.Equal(t, []byte("/baz"), path)
	assert.Equal(t, []byte("new_query"), query)
	assert.Equal(t, []byte("new_body"), body)
}

func TestLRU_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	cfg := newTestCfg(1 << 20)
	backend := dummyBackend{}
	cache := NewStorage(ctx, cfg, backend)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := mock.GenRndEntry(cfg, backend, []byte("/"+strconv.Itoa(i)))
			cache.Set(entry)
			cache.Get(entry)
		}(i)
	}
	wg.Wait()
	assert.Greater(t, cache.Len(), int64(0))
}
