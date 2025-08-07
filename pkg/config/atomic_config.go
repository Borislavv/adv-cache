package config

import (
	"sync/atomic"
)

type AtomicCache struct {
	env      *atomic.Pointer[string]
	enabled  *atomic.Bool
	api      *atomic.Pointer[Api]
	upstream *atomic.Pointer[Upstream]
	runtime  *atomic.Pointer[Runtime]
	data     *atomic.Pointer[Data]
	refresh  *atomic.Pointer[Refresh]
	eviction *atomic.Pointer[Eviction]
	storage  *atomic.Pointer[Storage]
	logs     *atomic.Pointer[Logs]
	k8s      *atomic.Pointer[K8S]
	metrics  *atomic.Pointer[Metrics]
	forceGC  *atomic.Pointer[ForceGC]
	rules    map[string]*atomic.Pointer[Rule]
}

func makeConfigAtomic(config *Cache) *AtomicCache {
	atomicCfg := &AtomicCache{
		env:      &atomic.Pointer[string]{},
		enabled:  &atomic.Bool{},
		api:      &atomic.Pointer[Api]{},
		upstream: &atomic.Pointer[Upstream]{},
		runtime:  &atomic.Pointer[Runtime]{},
		data:     &atomic.Pointer[Data]{},
		refresh:  &atomic.Pointer[Refresh]{},
		eviction: &atomic.Pointer[Eviction]{},
		storage:  &atomic.Pointer[Storage]{},
		logs:     &atomic.Pointer[Logs]{},
		k8s:      &atomic.Pointer[K8S]{},
		metrics:  &atomic.Pointer[Metrics]{},
		forceGC:  &atomic.Pointer[ForceGC]{},
		rules:    make(map[string]*atomic.Pointer[Rule], len(config.Cache.Rules)),
	}

	atomicCfg.env.Store(&config.Cache.Env)
	atomicCfg.enabled.Store(config.Cache.Enabled)
	atomicCfg.api.Store(config.Cache.Api)
	atomicCfg.upstream.Store(config.Cache.Upstream)
	atomicCfg.runtime.Store(config.Cache.Runtime)
	atomicCfg.data.Store(config.Cache.Data)
	atomicCfg.refresh.Store(config.Cache.Refresh)
	atomicCfg.eviction.Store(config.Cache.Eviction)
	atomicCfg.storage.Store(config.Cache.Storage)
	atomicCfg.logs.Store(config.Cache.Logs)
	atomicCfg.k8s.Store(config.Cache.K8S)
	atomicCfg.metrics.Store(config.Cache.Metrics)
	atomicCfg.forceGC.Store(config.Cache.ForceGC)

	for path, rule := range config.Cache.Rules {
		atomicCfg.rules[path] = &atomic.Pointer[Rule]{}
		atomicCfg.rules[path].Store(rule)
	}

	return atomicCfg
}

func (c *AtomicCache) IsProd() bool {
	return *c.env.Load() == Prod
}
func (c *AtomicCache) SetAsProd() {
	prod := Prod
	c.env.Store(&prod)
}

func (c *AtomicCache) IsDev() bool {
	return *c.env.Load() == Dev
}
func (c *AtomicCache) SetAsDev() {
	dev := Dev
	c.env.Store(&dev)
}

func (c *AtomicCache) IsTest() bool {
	return *c.env.Load() == Test
}
func (c *AtomicCache) SetAsTest() {
	test := Test
	c.env.Store(&test)
}

func (c *AtomicCache) IsEnabled() bool {
	return c.enabled.Load()
}
func (c *AtomicCache) SetEnabled(v bool) {
	c.enabled.Store(v)
}

func (c *AtomicCache) Runtime() *Runtime {
	return c.runtime.Load()
}
func (c *AtomicCache) SetRuntime(v *Runtime) {
	c.runtime.Store(v)
}

func (c *AtomicCache) Api() *Api {
	return c.api.Load()
}
func (c *AtomicCache) SetApi(v *Api) {
	c.api.Store(v)
}

func (c *AtomicCache) Upstream() *Upstream {
	return c.upstream.Load()
}
func (c *AtomicCache) SetUpstream(v *Upstream) {
	c.upstream.Store(v)
}

func (c *AtomicCache) Data() *Data {
	return c.data.Load()
}
func (c *AtomicCache) SetData(v *Data) {
	c.data.Store(v)
}

func (c *AtomicCache) Refresh() *Refresh {
	return c.refresh.Load()
}
func (c *AtomicCache) SetRefresh(v *Refresh) {
	c.refresh.Store(v)
}

func (c *AtomicCache) Eviction() *Eviction {
	return c.eviction.Load()
}
func (c *AtomicCache) SetEviction(v *Eviction) {
	c.eviction.Store(v)
}

func (c *AtomicCache) Storage() *Storage {
	return c.storage.Load()
}
func (c *AtomicCache) SetStorage(v *Storage) {
	c.storage.Store(v)
}

func (c *AtomicCache) Logs() *Logs {
	return c.logs.Load()
}
func (c *AtomicCache) SetLogs(v *Logs) {
	c.logs.Store(v)
}

func (c *AtomicCache) K8S() *K8S {
	return c.k8s.Load()
}
func (c *AtomicCache) SetK8S(v *K8S) {
	c.k8s.Store(v)
}

func (c *AtomicCache) Metrics() *Metrics {
	return c.metrics.Load()
}
func (c *AtomicCache) SetMetrics(v *Metrics) {
	c.metrics.Store(v)
}

func (c *AtomicCache) ForceGC() *ForceGC {
	return c.forceGC.Load()
}
func (c *AtomicCache) SetForceGC(v *ForceGC) {
	c.forceGC.Store(v)
}

func (c *AtomicCache) Rule(path string) (*atomic.Pointer[Rule], bool) {
	v, ok := c.rules[path]
	return v, ok
}
func (c *AtomicCache) SetRule(path string, v *Rule) bool {
	if _, ok := c.rules[path]; !ok {
		return false
	}

	c.rules[path].Store(v)

	return true
}
