package storage

import (
	"github.com/rs/zerolog"
	"runtime"
	"testing"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/dgraph-io/ristretto"
)

var path = []byte("/api/v2/pagedata")

func init() {
	zerolog.SetGlobalLevel(zerolog.ErrorLevel)
}

type RistrettoStorage struct {
	c *ristretto.Cache
}

func NewRistrettoStorage(maxCost int64) *RistrettoStorage {
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 10_000_000,
		MaxCost:     maxCost,
		BufferItems: 256,
		Metrics:     true,
	})
	if err != nil {
		panic(err)
	}
	return &RistrettoStorage{c: cache}
}

func (r *RistrettoStorage) Set(resp *model.Response) {
	r.c.Set(resp.MapKey(), resp, resp.Weight())
}

func (r *RistrettoStorage) Get(req *model.Request) (*model.Response, bool) {
	val, ok := r.c.Get(req.MapKey())
	if !ok {
		return nil, false
	}
	return val.(*model.Response), true
}

var ristrettoCfg *config.Cache

func init() {
	ristrettoCfg = &config.Cache{
		Cache: config.CacheBox{
			Enabled: true,
			Preallocate: config.Preallocation{
				PerShard: 8196,
			},
			Eviction: config.Eviction{
				Policy:    "lru",
				Threshold: 0.9,
			},
			Refresh: config.Refresh{
				TTL:        time.Hour,
				ErrorTTL:   time.Minute * 10,
				Beta:       0.4,
				MinStale:   time.Minute * 40,
				BackendURL: "https://seo-master.lux.kube.xbet.lan",
			},
			Storage: config.Storage{
				Type: "malloc",
				Size: 1024 * 1024 * 64, // 64 MB
			},
		},
	}
}

func reportMemAndRistretto(b *testing.B, store *RistrettoStorage) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	b.ReportMetric(float64(mem.Alloc)/1024/1024, "allocsMB")

	stats := store.c.Metrics
	b.ReportMetric(float64(stats.CostAdded())/1024/1024, "ristrettoMB")
}

// -- BENCHMARKS

func BenchmarkRistrettoRead1000x(b *testing.B) {
	store := NewRistrettoStorage(int64(ristrettoCfg.Cache.Storage.Size))
	responses := mock.GenerateRandomResponses(ristrettoCfg, path, b.N+1)

	for _, resp := range responses {
		store.Set(resp)
	}
	length := len(responses)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			for j := 0; j < 1000; j++ {
				_, _ = store.Get(responses[(i*j)%length].Request())
			}
			i += 1000
		}
	})
	b.StopTimer()

	reportMemAndRistretto(b, store)
}

func BenchmarkRistrettoWrite1000x(b *testing.B) {
	store := NewRistrettoStorage(int64(ristrettoCfg.Cache.Storage.Size))
	responses := mock.GenerateRandomResponses(ristrettoCfg, path, b.N+1)
	length := len(responses)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			for j := 0; j < 1000; j++ {
				store.Set(responses[(i*j)%length])
			}
			i += 1000
		}
	})
	b.StopTimer()

	reportMemAndRistretto(b, store)
}

func BenchmarkRistrettoGetAllocs(b *testing.B) {
	store := NewRistrettoStorage(int64(ristrettoCfg.Cache.Storage.Size))
	resp := mock.GenerateRandomResponses(ristrettoCfg, path, 1)[0]
	store.Set(resp)
	req := resp.Request()

	allocs := testing.AllocsPerRun(100_000, func() {
		_, _ = store.Get(req)
	})
	b.ReportMetric(allocs, "allocs/op")

	reportMemAndRistretto(b, store)
}

func BenchmarkRistrettoSetAllocs(b *testing.B) {
	store := NewRistrettoStorage(int64(ristrettoCfg.Cache.Storage.Size))
	resp := mock.GenerateRandomResponses(ristrettoCfg, path, 1)[0]

	allocs := testing.AllocsPerRun(100_000, func() {
		store.Set(resp)
	})
	b.ReportMetric(allocs, "allocs/op")

	reportMemAndRistretto(b, store)
}
