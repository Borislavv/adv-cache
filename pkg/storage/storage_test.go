package storage

import (
	"context"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/rs/zerolog"
)

const (
	maxEntriesNum = 100_000
	maxRetriesNum = 1_000_000
)

var (
	path = []byte("/api/v2/pagedata")
)

var cfg *config.Cache

func init() {
	cfg = &config.Cache{
		Cache: &config.CacheBox{
			Enabled: true,
			LifeTime: config.Lifetime{
				MaxReqDuration:             time.Millisecond * 100,
				EscapeMaxReqDurationHeader: "X-Target-Bot",
			},
			Proxy: &config.Proxy{
				FromUrl: []byte("https://google.com"),
				Rate:    1000,
				Timeout: time.Second * 5,
			},
			Preallocate: config.Preallocation{
				PerShard: 8,
			},
			Eviction: &config.Eviction{
				Enabled:   true,
				Threshold: 0.9,
			},
			Refresh: &config.Refresh{
				TTL:  time.Hour,
				Beta: 0.4,
			},
			Storage: &config.Storage{
				Type: "malloc",
				Size: 1024 * 500000, // 5 MB
			},
			Rules: map[string]*config.Rule{
				"/api/v2/pagedata": {
					PathBytes: []byte("/api/v2/pagedata"),
					Refresh: &config.RuleRefresh{
						Enabled:     true,
						TTL:         time.Hour,
						Beta:        0.5,
						Coefficient: 0.5,
					},
					CacheKey: config.RuleKey{
						Query:      []string{"project[id]", "domain", "language", "choice"},
						QueryBytes: [][]byte{[]byte("project[id]"), []byte("domain"), []byte("language"), []byte("choice")},
						Headers:    []string{"Accept-Encoding", "Accept-Language"},
						HeadersMap: map[string]struct{}{
							"Accept-Encoding": {},
							"Accept-Language": {},
						},
					},
					CacheValue: config.RuleValue{
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

func BenchmarkReadFromStorage1000TimesPerIter(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := repository.NewBackend(ctx, cfg)
	db := lru.NewStorage(ctx, cfg, backend)

	entries := mock.GenerateEntryPointersConsecutive(cfg, backend, path, maxEntriesNum)
	for _, entry := range entries {
		db.Set(entry)
	}
	length := len(entries)

	var ok int64
	var total int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			for j := 0; j < 1000; j++ {
				db.Get(entries[(i*j)%length])
			}
			i += 1000
		}
	})
	b.StopTimer()

	if atomic.LoadInt64(&ok) != atomic.LoadInt64(&total) {
		panic(fmt.Sprintf("BenchmarkReadFromStorage1000TimesPerIter: total[%d] != ok[%d]", atomic.LoadInt64(&total), atomic.LoadInt64(&ok)))
	}
}

func BenchmarkWriteIntoStorage1000TimesPerIter(b *testing.B) {
	ctx, cancel := context.WithCancel(b.Context())
	defer cancel()

	backend := repository.NewBackend(ctx, cfg)
	db := lru.NewStorage(ctx, cfg, backend)

	entries := mock.GenerateEntryPointersConsecutive(cfg, backend, path, maxEntriesNum)
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

	entry := mock.GenerateRandomEntryPointer(cfg, backend, path)
	db.Set(entry)

	b.StartTimer()
	allocs := testing.AllocsPerRun(maxRetriesNum, func() {
		db.Get(entry)
	})
	b.StopTimer()
	b.ReportMetric(allocs, "allocs/op")
}

func BenchmarkSetAllocs(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := repository.NewBackend(ctx, cfg)
	db := lru.NewStorage(ctx, cfg, backend)

	entry := mock.GenerateRandomEntryPointer(cfg, backend, path)

	b.StartTimer()
	allocs := testing.AllocsPerRun(maxRetriesNum, func() {
		db.Set(entry)
	})
	b.StopTimer()
	b.ReportMetric(allocs, "allocs/op")
}
