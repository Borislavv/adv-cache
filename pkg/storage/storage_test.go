package storage

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/repository"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/cache/lru"
	synced "github.com/Borislavv/traefik-http-cache-plugin/pkg/sync"
	"testing"
	"time"

	"github.com/Borislavv/traefik-http-cache-plugin/pkg/config"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/mock"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/cache"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
)

// BenchmarkReadFromStorage1000TimesPerIter benchmarks parallel cache reads with profile collection.
// Each iteration does 1000 Get() calls with different requests.
func BenchmarkReadFromStorage1000TimesPerIter(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &config.Cache{
		AppEnv:                    "dev",
		AppDebug:                  false,
		BackendUrl:                "https://seo-master.lux.kube.xbet.lan/api/v2/pagedata",
		RevalidateBeta:            0.3,
		RevalidateInterval:        time.Hour,
		InitStorageLengthPerShard: 768,
		EvictionAlgo:              string(cache.LRU),
		MemoryFillThreshold:       0.95,
		MemoryLimit:               1024 * 1024 * 1024, // 3GB
	}

	shardedMap := sharded.NewMap[*model.Response](cfg.InitStorageLengthPerShard)
	balancer := lru.NewBalancer(ctx, shardedMap)
	refresher := lru.NewRefresher(ctx, cfg, balancer)
	backend := repository.NewBackend(cfg, synced.NewPooledResponseReader(2048))
	db := New(ctx, cfg, balancer, refresher, backend, shardedMap)

	responses := mock.GenerateRandomResponses(cfg, b.N+1)
	for _, resp := range responses {
		db.Set(resp)
	}
	length := len(responses)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			for j := 0; j < 10000; j++ {
				db.Get(responses[(i*j)%length].Request())
			}
			i += 10000
		}
	})
	b.StopTimer()
}

// BenchmarkWriteIntoStorage1000TimesPerIter benchmarks parallel cache writes with profile collection.
// Each iteration does 1000 Set() calls.
func BenchmarkWriteIntoStorage1000TimesPerIter(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &config.Cache{
		AppEnv:                    "dev",
		AppDebug:                  false,
		BackendUrl:                "https://seo-master.lux.kube.xbet.lan/api/v2/pagedata",
		RevalidateBeta:            0.3,
		RevalidateInterval:        time.Hour,
		InitStorageLengthPerShard: 768,
		EvictionAlgo:              string(cache.LRU),
		MemoryFillThreshold:       0.95,
		MemoryLimit:               1024 * 1024 * 1024, // 3GB
	}

	shardedMap := sharded.NewMap[*model.Response](cfg.InitStorageLengthPerShard)
	balancer := lru.NewBalancer(ctx, shardedMap)
	refresher := lru.NewRefresher(ctx, cfg, balancer)
	backend := repository.NewBackend(cfg, synced.NewPooledResponseReader(2048))
	db := New(ctx, cfg, balancer, refresher, backend, shardedMap)

	responses := mock.GenerateRandomResponses(cfg, b.N+1)
	length := len(responses)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			for j := 0; j < 10000; j++ {
				db.Set(responses[(i*j)%length])
			}
			i += 10000
		}
	})
	b.StopTimer()
}

// BenchmarkGetAllocs benchmarks allocation count per Get() call.
func BenchmarkGetAllocs(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &config.Cache{
		BackendUrl:                "https://seo-master.lux.kube.xbet.lan/api/v2/pagedata",
		RevalidateBeta:            0.3,
		RevalidateInterval:        time.Hour,
		InitStorageLengthPerShard: 256,
		EvictionAlgo:              string(cache.LRU),
		MemoryFillThreshold:       0.95,
		MemoryLimit:               1024 * 1024 * 256, // 1MB
	}

	shardedMap := sharded.NewMap[*model.Response](cfg.InitStorageLengthPerShard)
	balancer := lru.NewBalancer(ctx, shardedMap)
	refresher := lru.NewRefresher(ctx, cfg, balancer)
	backend := repository.NewBackend(cfg, synced.NewPooledResponseReader(2048))
	db := New(ctx, cfg, balancer, refresher, backend, shardedMap)

	resp := mock.GenerateRandomResponses(cfg, 1)[0]
	db.Set(resp)
	req := resp.Request()

	allocs := testing.AllocsPerRun(100_000, func() {
		db.Get(req)
	})
	b.ReportMetric(allocs, "allocs/op")
}

// BenchmarkSetAllocs benchmarks allocation count per Set() call.
func BenchmarkSetAllocs(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &config.Cache{
		BackendUrl:                "https://seo-master.lux.kube.xbet.lan/api/v2/pagedata",
		RevalidateBeta:            0.3,
		RevalidateInterval:        time.Hour,
		InitStorageLengthPerShard: 256,
		EvictionAlgo:              string(cache.LRU),
		MemoryFillThreshold:       0.95,
		MemoryLimit:               1024 * 1024 * 256, // 1MB
	}

	shardedMap := sharded.NewMap[*model.Response](cfg.InitStorageLengthPerShard)
	balancer := lru.NewBalancer(ctx, shardedMap)
	refresher := lru.NewRefresher(ctx, cfg, balancer)
	backend := repository.NewBackend(cfg, synced.NewPooledResponseReader(2048))
	db := New(ctx, cfg, balancer, refresher, backend, shardedMap)

	resp := mock.GenerateRandomResponses(cfg, 1)[0]

	allocs := testing.AllocsPerRun(100_000, func() {
		db.Set(resp)
	})
	b.ReportMetric(allocs, "allocs/op")
}
