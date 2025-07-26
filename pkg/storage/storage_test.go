package storage

import (
	"context"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/list"
	"github.com/Borislavv/advanced-cache/pkg/model"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"testing"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/rs/zerolog"
)

const maxEntriesNum = 1_000_000

var path = []byte("/api/v2/pagedata")
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
				Enabled:   true,
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
				Size: 1024 * 1024 * 5, // 5 MB
			},
			Rules: map[string]*config.Rule{
				"/api/v2/pagedata": {
					PathBytes: []byte("/api/v2/pagedata"),
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

func BenchmarkReadFromStorage1000TimesPerIter(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := repository.NewBackend(ctx, cfg)
	db := NewStorage(ctx, cfg, backend)

	numEntries := b.N + 1
	if numEntries > maxEntriesNum {
		numEntries = maxEntriesNum
	}

	entries := mock.GenerateEntryPointersConsecutive(cfg, backend, path, numEntries)
	sets := make([]*model.VersionPointer, 0, 1024)
	for _, resp := range entries {
		if persistedEntry, wasPersisted := db.Set(resp); wasPersisted {
			sets = append(sets, persistedEntry)
			persistedEntry.Release()
		} else {
			persistedEntry.Remove()
		}
	}
	entries = sets
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
	db := NewStorage(ctx, cfg, backend)

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
				entry, persisted := db.Set(entries[(i*j)%length])
				if !persisted {
					entry.Remove()
				} else {
					entry.Release()
				}
			}
			i += 1000
		}
	})
	b.StopTimer()

	//EvictNotFoundMostLoaded = &atomic.Int64{}
	//EvictListIsZero         = &atomic.Int64{}
	//EvictNextNotFound       = &atomic.Int64{}
	//EvictNotAcquired        = &atomic.Int64{}
	//EvictTotalRemove        = &atomic.Int64{}
	//EvictRemoveHits         = &atomic.Int64{}

	fmt.Printf("acquired: %d, released: %d, finalized: %d, nilList: %d\n",
		list.Acquired.Load(), list.Released.Load(), model.Finzalized.Load(), model.NilList.Load())

	fmt.Printf("notFoundMostLoaded: %d, lruListIsEmpty: %d, nextIsNotFound: %d, notAcquired: %d, totalRemove: %d, realRemoveHits: %d, reamoveHits: %d\n",
		EvictNotFoundMostLoaded.Load(), EvictListIsZero.Load(), EvictNextNotFound.Load(), EvictNotAcquired.Load(), EvictTotalRemove.Load(), sharded.EvictRemoveHits.Load(), RealEvictionFinalized.Load())

	time.Sleep(time.Second)
}

func BenchmarkGetAllocs(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := repository.NewBackend(ctx, cfg)
	db := NewStorage(ctx, cfg, backend)

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
	db := NewStorage(ctx, cfg, backend)

	entry := mock.GenerateEntryPointersConsecutive(cfg, backend, path, 1)[0]

	allocs := testing.AllocsPerRun(100_000, func() {
		db.Set(entry)
	})
	b.ReportMetric(allocs, "allocs/op")
}
