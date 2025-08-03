package storage

import (
	"context"
	"testing"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"github.com/stretchr/testify/assert"
)

type dummyBackend struct{}

func (d dummyBackend) Fetch(*config.Rule, []byte, []byte, *[][2][]byte) (int, *[][2][]byte, []byte, func(), error) {
	return 200, nil, nil, func() {}, nil
}

func (d dummyBackend) RevalidatorMaker() func(*config.Rule, []byte, []byte, *[][2][]byte) (int, *[][2][]byte, []byte, func(), error) {
	return func(*config.Rule, []byte, []byte, *[][2][]byte) (int, *[][2][]byte, []byte, func(), error) {
		return 200, nil, nil, func() {}, nil
	}
}

func newCfg(tmpDir string) *config.Cache {
	return &config.Cache{Cache: &config.CacheBox{
		Enabled:     true,
		Storage:     &config.Storage{Type: "malloc", Size: 1 << 20},
		Eviction:    &config.Eviction{Enabled: true, Threshold: 0.9},
		Preallocate: config.Preallocation{Shards: 1, PerShard: 0},
		Persistence: &config.Persistence{Dump: &config.Dump{IsEnabled: true, Dir: tmpDir, MaxVersions: 1, Gzip: false, Crc32Control: true}},
	}}
}

func TestDumpLoad_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := newCfg(tmpDir)
	backend := dummyBackend{}
	ctx := context.Background()

	db := lru.NewStorage(ctx, cfg, backend)
	dumper := storage.NewDumper(cfg, db, backend)

	// fill cache
	entry := mock.GenRndEntry(cfg, backend, []byte("/dump"))
	db.Set(entry)

	// dump
	assert.NoError(t, dumper.Dump(ctx))

	// clear & load
	db.Clear()
	assert.Equal(t, int64(0), db.Len())
	assert.NoError(t, dumper.Load(ctx))

	// verify
	_, found := db.Get(entry)
	assert.True(t, found)
}

func TestDumpDisabled_Error(t *testing.T) {
	cfg := &config.Cache{Cache: &config.CacheBox{Enabled: true, Persistence: &config.Persistence{Dump: &config.Dump{IsEnabled: false}}}}
	backend := dummyBackend{}
	ctx := context.Background()
	db := lru.NewStorage(ctx, cfg, backend)
	dumper := NewDumper(cfg, db, backend)

	err := dumper.Dump(ctx)
	assert.ErrorIs(t, err, ErrDumpNotEnabled)
}
