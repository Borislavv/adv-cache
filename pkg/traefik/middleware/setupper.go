package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"github.com/Borislavv/advanced-cache/pkg/traefik/middleware/router"
	"github.com/Borislavv/advanced-cache/pkg/traefik/middleware/router/route"
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

	m.router = router.NewRouter(
		route.NewUpstream(backend),
		route.NewCacheRoutes(cacheCfg, db, backend),
		route.NewClearRoute(cacheCfg, db),
		route.NewK8sProbeRoute(),
		route.NewEnableRoute(),
		route.NewDisableRoute(),
		route.NewMetricsRoute(),
	)

	// run additional workers
	NewMetricsLogger(ctx, cacheCfg, db, meter).run()

	// load data if necessary
	LoadDumpIfNecessary(ctx)
	m.loadMocksIfNecessary(ctx, backend, db)

	// tell everyone that cache is enabled
	route.EnableCache()
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
