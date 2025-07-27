package storage

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"runtime"
	"testing"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/rs/zerolog"
)

const maxEntriesNum = 1_000_000

var cfg *config.Cache

func init() {
	cfg = &config.Cache{
		Cache: config.CacheBox{
			Enabled: true,
			LifeTime: config.Lifetime{
				MaxReqDuration:             time.Millisecond * 100,
				EscapeMaxReqDurationHeader: "X-Target-Bot",
			},
			Proxy: config.Proxy{
				FromUrl: []byte("https://google.com"),
				Rate:    1000,
				Timeout: time.Second * 5,
			},
			Preallocate: config.Preallocation{
				PerShard: 8,
			},
			Eviction: config.Eviction{
				Policy:    "lru",
				Threshold: 0.9,
			},
			Refresh: config.Refresh{
				TTL:      time.Hour,
				ErrorTTL: time.Minute * 10,
				Beta:     0.4,
				MinStale: time.Minute * 40,
			},
			Storage: config.Storage{
				Type: "malloc",
				Size: 1024 * 500, // 5 MB
			},
			Rules: map[string]*config.Rule{
				"/api/v2/pagedata": {
					PathBytes: []byte("/api/v2/pagedata"),
					TTL:       time.Hour,
					ErrorTTL:  time.Minute * 15,
					Beta:      0.4,
					MinStale:  time.Duration(float64(time.Hour) * 0.4),
					CacheKey: config.Key{
						Query:      []string{"project[id]", "domain", "language", "choice"},
						QueryBytes: [][]byte{[]byte("project[id]"), []byte("domain"), []byte("language"), []byte("choice")},
						Headers:    []string{"Accept-Encoding", "Accept-Language"},
						HeadersMap: map[string]struct{}{
							"Accept-Encoding": {},
							"Accept-Language": {},
						},
					},
					CacheValue: config.Value{
						Headers: []string{"Content-Type", "Vary"},
						HeadersMap: map[string]struct{}{
							"Content-Type": {},
							"Vary":         {},
						},
					},
				},
			},
		},
	}

	zerolog.SetGlobalLevel(zerolog.ErrorLevel)
}

func reportMemAndAdvancedCache(b *testing.B, usageMem int64) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	b.ReportMetric(float64(mem.Alloc)/1024/1024, "allocsMB")
	b.ReportMetric(float64(usageMem)/1024/1024, "advancedCacheMB")
}

func BenchmarkReadFromStorage1000TimesPerIter(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := repository.NewBackend(ctx, cfg)
	db := lru.NewStorage(ctx, cfg, backend)

	numEntries := b.N + 1
	if numEntries > maxEntriesNum {
		numEntries = maxEntriesNum
	}

	entries := mock.GenerateEntryPointersConsecutive(cfg, backend, path, numEntries)
	for _, resp := range entries {
		db.Set(resp)
	}
	length := len(entries)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			for j := 0; j < 1000; j++ {
				db.Get(entries[(i*j)%length].Entry)
			}
			i += 1000
		}
	})
	b.StopTimer()
}

func BenchmarkWriteIntoStorage1000TimesPerIter(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := repository.NewBackend(ctx, cfg)
	db := lru.NewStorage(ctx, cfg, backend)

	numEntries := b.N + 1
	if numEntries > maxEntriesNum {
		numEntries = maxEntriesNum
	}

	entries := mock.GenerateEntryPointersConsecutive(cfg, backend, path, numEntries)
	length := len(entries)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			for j := 0; j < 1000; j++ {
				db.Set(entries[(i*j)%length])
			}
			i += 1000
		}
	})
	b.StopTimer()
}

func BenchmarkGetAllocs(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := repository.NewBackend(ctx, cfg)
	db := lru.NewStorage(ctx, cfg, backend)

	entry := mock.GenerateEntryPointersConsecutive(cfg, backend, path, 1)[0]
	db.Set(entry)

	allocs := testing.AllocsPerRun(100_000, func() {
		db.Get(entry.Entry)
	})
	b.ReportMetric(allocs, "allocs/op")
}

func BenchmarkSetAllocs(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := repository.NewBackend(ctx, cfg)
	db := lru.NewStorage(ctx, cfg, backend)

	entry := mock.GenerateEntryPointersConsecutive(cfg, backend, path, 1)[0]

	allocs := testing.AllocsPerRun(100_000, func() {
		db.Set(entry)
	})
	b.ReportMetric(allocs, "allocs/op")
}
