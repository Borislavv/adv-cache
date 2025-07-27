package storage

import (
	"context"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"testing"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/rs/zerolog"
)

const (
	maxEntriesNum = 1_000_000
)

var (
	path = []byte("/api/v2/pagedata")
)

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

//func BenchmarkReadFromStorage1000TimesPerIter(b *testing.B) {
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	backend := repository.NewBackend(ctx, cfg)
//	db := lru.NewStorage(ctx, cfg, backend)
//
//	numEntries := b.N + 1
//	if numEntries > maxEntriesNum {
//		numEntries = maxEntriesNum
//	}
//
//	entries := mock.GenerateEntryPointersConsecutive(cfg, backend, path, numEntries)
//	sets := entries[:0]
//	for _, resp := range entries {
//		if entry, ok := db.Set(resp); ok {
//			sets = append(sets, entry)
//			entry.Release()
//		} else {
//			resp.Remove()
//		}
//	}
//	entries = sets
//	length := len(entries)
//
//	b.ResetTimer()
//	b.RunParallel(func(pb *testing.PB) {
//		i := 0
//		for pb.Next() {
//			for j := 0; j < 1000; j++ {
//				if entry, found := db.Get(entries[(i*j)%length].Entry); found {
//					entry.Release()
//				}
//			}
//			i += 1000
//		}
//	})
//	b.StopTimer()
//
//	//Acquired  = &atomic.Int64{}
//	//Released  = &atomic.Int64{}
//	//Finalized = &atomic.Int64{}
//
//	i := 0
//	for _, entry := range entries {
//		if i > 25 {
//			break
//		}
//		entry.Remove()
//		fmt.Printf(
//			"BenchmarkReadFromStorage1000TimesPerIter[%d]: version: %d, refCount: %d, isDoomed: %v\n",
//			b.N, entry.Version(), entry.RefCount(), entry.IsDoomed(),
//		)
//		i++
//	}
//
//	fmt.Printf(
//		"BenchmarkReadFromStorage1000TimesPerIter[%d]: acq: %d, rel: %d, fin: %d\n",
//		b.N, model.Acquired.Load(), model.Released.Load(), model.Finalized.Load(),
//	)
//}

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
				inserted, _ := db.Set(entries[(i*j)%length])
				inserted.Release()
			}
			i += 1000
		}
	})
	b.StopTimer()

	fmt.Printf(
		"before-remove-BenchmarkWriteIntoStorage1000TimesPerIter[%d]: acq: %d, rel: %d, rem: %d, fin: %d\n",
		b.N, model.Acquired.Load(), model.Released.Load(), model.Removed.Load(), model.Finalized.Load(),
	)

	i := 0
	for _, entry := range entries {
		entry.Remove()
		if i > 5 {
			continue
		}
		fmt.Printf(
			"BenchmarkWriteIntoStorage1000TimesPerIter[%d]: version: %d, refCount: %d, isDoomed: %v\n",
			b.N, entry.Version(), entry.RefCount(), entry.IsDoomed(),
		)
		i++
	}

	fmt.Printf(
		"after-remove-BenchmarkWriteIntoStorage1000TimesPerIter[%d]: acq: %d, rel: %d, rem: %d, fin: %d\n",
		b.N, model.Acquired.Load(), model.Released.Load(), model.Removed.Load(), model.Finalized.Load(),
	)
}

//func BenchmarkGetAllocs(b *testing.B) {
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	backend := repository.NewBackend(ctx, cfg)
//	db := lru.NewStorage(ctx, cfg, backend)
//
//	entry := mock.GenerateEntryPointersConsecutive(cfg, backend, path, 1)[0]
//	if _, persisted := db.Set(entry); !persisted {
//		panic("failed to persist entry")
//	} else {
//		entry.Release()
//	}
//
//	b.StartTimer()
//	allocs := testing.AllocsPerRun(100_000, func() {
//		if cachedValue, found := db.Get(entry.Entry); found {
//			cachedValue.Release()
//		}
//	})
//	b.StopTimer()
//	b.ReportMetric(allocs, "allocs/op")
//
//	entry.Remove()
//
//	time.Sleep(time.Second)
//
//	fmt.Printf(
//		"BenchmarkGetAllocs[%d]: version: %d, refCount: %d, isDoomed: %v, acq: %d, rel: %d, fin: %d\n",
//		b.N, entry.Version(), entry.RefCount(), entry.IsDoomed(), model.Acquired.Load(), model.Released.Load(), model.Finalized.Load(),
//	)
//}
//
//func BenchmarkSetAllocs(b *testing.B) {
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	backend := repository.NewBackend(ctx, cfg)
//	db := lru.NewStorage(ctx, cfg, backend)
//
//	entry := mock.GenerateEntryPointersConsecutive(cfg, backend, path, 1)[0]
//
//	b.StartTimer()
//	allocs := testing.AllocsPerRun(100_000, func() {
//		if inserted, persisted := db.Set(entry); !persisted {
//			panic("failed to persist entry")
//		} else {
//			inserted.Release()
//		}
//	})
//	b.StopTimer()
//	b.ReportMetric(allocs, "allocs/op")
//
//	entry.Remove()
//
//	time.Sleep(time.Second)
//
//	fmt.Printf(
//		"BenchmarkSetAllocs[%d]: version: %d, refCount: %d, isDoomed: %v, acq: %d, rel: %d, fin: %d\n",
//		b.N, entry.Version(), entry.RefCount(), entry.IsDoomed(), model.Acquired.Load(), model.Released.Load(), model.Finalized.Load(),
//	)
//}
