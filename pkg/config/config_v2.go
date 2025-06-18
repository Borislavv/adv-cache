package config

import "time"

type Config struct {
	Cache CacheConfig `yaml:"cache"`
}

type CacheConfig struct {
	Enabled  bool              `yaml:"enabled"`
	Eviction EvictionConfig    `yaml:"eviction"`
	Storage  StorageConfig     `yaml:"storage"`
	Refresh  RefreshConfig     `yaml:"refresh"`
	Rules    []CacheRuleConfig `yaml:"rules"`
}

type EvictionConfig struct {
	Policy    string  `yaml:"policy"`    // "lru", "lfu", etc.
	Threshold float64 `yaml:"threshold"` // 0.9 means 90%
}

type StorageConfig struct {
	Type string `yaml:"type"` // "malloc"
	Size string `yaml:"size"` // string to allow large numbers like "21474836480"
}

type RefreshConfig struct {
	TTL  time.Duration `yaml:"ttl"`  // e.g. "1d"
	Beta float64       `yaml:"beta"` // between 0 and 1
}

type CacheRuleConfig struct {
	Path       string     `yaml:"path"`
	CacheKey   CacheKey   `yaml:"cache_key"`
	CacheValue CacheValue `yaml:"cache_value"`
}

type CacheKey struct {
	Query   []string `yaml:"query"`
	Headers []string `yaml:"headers"`
}

type CacheValue struct {
	Headers []string `yaml:"headers"`
}
