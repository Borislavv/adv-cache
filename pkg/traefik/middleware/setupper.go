package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	route2 "github.com/Borislavv/advanced-cache/pkg/router/route"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"github.com/rs/zerolog/log"
)

var (
	cacheCfg    *config.Cache
	cacheDumper storage.Dumper
)

func (m *TraefikCacheMiddleware) setUpCache() {
	ctx := m.ctx
	cacheCfg = m.cfg

	// build dependencies
	meter := metrics.New()
	backend := repository.NewBackend(ctx, cacheCfg)
	db := lru.NewStorage(ctx, cacheCfg, backend)
	cacheDumper = storage.NewDumper(cacheCfg, db, backend)

	//m.router = router.NewRouter(
	//	route2.NewUpstream(backend),
	//	route2.NewCacheRoutes(cacheCfg, db, backend),
	//	route2.NewClearRoute(cacheCfg, db),
	//	route2.NewK8sProbeRoute(),
	//	route2.NewEnableRoute(),
	//	route2.NewDisableRoute(),
	//	route2.NewMetricsRoute(),
	//)

	// run additional workers
	NewMetricsLogger(ctx, cacheCfg, db, meter).Run()

	// load data if necessary
	LoadDumpIfNecessary(ctx)
	m.loadMocksIfNecessary(ctx, backend, db)

	// tell everyone that cache is enabled
	route2.EnableCache()
}

func (m *TraefikCacheMiddleware) loadMocksIfNecessary(ctx context.Context, backend repository.Backender, db storage.Storage) {
	if cacheCfg.Cache.Persistence.Mock.Enabled {
		storage.LoadMocks(ctx, cacheCfg, backend, db, cacheCfg.Cache.Persistence.Mock.Length)
	}
}

func LoadDumpIfNecessary(ctx context.Context) {
	if cacheCfg.Cache.Enabled && cacheCfg.Cache.Persistence.Dump.IsEnabled {
		if err := cacheDumper.Load(ctx); err != nil {
			log.Error().Err(err).Msg("[dump] failed to load cache dump")
		}
	}
}

func StoreDumpIfNecessary(ctx context.Context) {
	if cacheCfg.Cache.Enabled && cacheCfg.Cache.Persistence.Dump.IsEnabled {
		if err := cacheDumper.Dump(ctx); err != nil {
			log.Error().Err(err).Msg("[dump] failed to store cache dump")
		}
	}
}
