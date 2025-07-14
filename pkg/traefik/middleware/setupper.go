package middleware

import (
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lfu"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
)

func (m *TraefikCacheMiddleware) setUpCache() {
	shardedMap := sharded.NewMap[*model.Entry](m.ctx, m.cfg.Cache.Preallocate.PerShard)
	m.backend = repository.NewBackend(m.cfg)
	balancer := lru.NewBalancer(m.ctx, shardedMap)
	tinyLFU := lfu.NewTinyLFU(m.ctx)
	m.store = lru.NewStorage(m.ctx, m.cfg, balancer, m.backend, tinyLFU, shardedMap)
	m.refresher = storage.NewRefresher(m.ctx, m.cfg, balancer, m.store)
	m.dumper = storage.NewDumper(m.cfg, shardedMap, m.store, m.backend)
	m.evictor = storage.NewEvictor(m.ctx, m.cfg, m.store, balancer)
}
