package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"time"
)

type V2 struct {
	Cache CacheV2 `yaml:"cache"`
}

type CacheV2 struct {
	Enabled  bool          `yaml:"enabled"`
	Eviction EvictionV2    `yaml:"eviction"`
	Refresh  RefreshV2     `yaml:"refresh"`
	Storage  StorageV2     `yaml:"storage"`
	Rules    []CacheRuleV2 `yaml:"rules"`
}

type EvictionV2 struct {
	Policy    string  `yaml:"policy"`    // "lru", "lfu", etc.
	Threshold float64 `yaml:"threshold"` // 0.9 means 90%
}

type StorageV2 struct {
	Type string `yaml:"type"` // "malloc"
	Size string `yaml:"size"` // 21474836480=2gb(bytes)
}

type RefreshV2 struct {
	// TTL - refresh TTL (max time life of response item in cache without refreshing).
	TTL time.Duration `yaml:"ttl"` // e.g. "1d" (responses with 200 status code)
	// ErrorTTL - error refresh TTL (max time life of response item with non 200 status code in cache without refreshing).
	ErrorTTL time.Duration `yaml:"error_ttl"` // e.g. "1h" (responses with non 200 status code)
	// beta определяет коэффициент, используемый для вычисления случайного момента обновления кэша.
	// Чем выше beta, тем чаще кэш будет обновляться до истечения TTL.
	// Формула взята из подхода "stochastic cache expiration" (см. Google Staleness paper):
	// expireTime = ttl * (-beta * ln(random()))
	// Подробнее: RFC 5861 и https://web.archive.org/web/20100829170210/http://labs.google.com/papers/staleness.pdf
	// beta: "0.4"
	Beta float64 `yaml:"beta"` // between 0 and 1
}

type CacheRuleV2 struct {
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

func LoadConfigV2(path string) (*V2, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config yaml file: %w", err)
	}

	var cfg V2
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}

	return &cfg, nil
}
