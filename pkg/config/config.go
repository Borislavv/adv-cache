package config

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"time"
)

const (
	Prod = "prod"
	Dev  = "dev"
	Test = "test"
)

type Cache struct {
	Cache CacheBox `yaml:"cache"`
}

type CacheBox struct {
	Enabled     bool          `yaml:"enabled"`
	Preallocate Preallocation `yaml:"preallocate"`
	Eviction    Eviction      `yaml:"eviction"`
	Refresh     Refresh       `yaml:"refresh"`
	Storage     Storage       `yaml:"storage"`
	Rules       []*CacheRule  `yaml:"rules"`
}

type Preallocation struct {
	PerShard int `yaml:"per_shard"`
}

type Eviction struct {
	Policy    string  `yaml:"policy"`    // at now, it's only "lru" with works
	Threshold float64 `yaml:"threshold"` // 0.9 means 90%
}

type Storage struct {
	Type string `yaml:"type"` // "malloc"
	Size uint   `yaml:"size"` // 21474836480=2gb(bytes)
}

type Refresh struct {
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
	Beta       float64       `yaml:"beta"`      // between 0 and 1
	MinStale   time.Duration `yaml:"min_stale"` // computed=time.Duration(float64(TTL/ErrorTTL) * Beta)
	BackendURL string        `yaml:"backend_url"`
}

type CacheRule struct {
	Path       string `yaml:"path"`
	PathBytes  []byte
	CacheKey   CacheKey   `yaml:"cache_key"`
	CacheValue CacheValue `yaml:"cache_value"`
}

type CacheKey struct {
	Query        []string `yaml:"query"` // Параметры, которые будут участвовать в ключе кэширования
	QueryBytes   [][]byte
	Headers      []string `yaml:"headers"` // Хедеры, которые будут участвовать в ключе кэширования
	HeadersBytes [][]byte
}

type CacheValue struct {
	Headers      []string `yaml:"headers"` // Хедеры ответа, которые будут сохранены в кэше вместе с body
	HeadersBytes [][]byte
}

const (
	configPath      = "/config/config.yaml"
	configPathLocal = "/config/config.local.yaml"
	configPathTest  = "/../../config/config.test.yaml"
)

func LoadConfig() (*Cache, error) {
	env := os.Getenv("APP_ENV")

	var path string
	switch {
	case env == Prod:
		path = configPath
	case env == Dev:
		path = configPathLocal
	case env == Test:
		path = configPathTest
	default:
		return nil, errors.New("unknown APP_ENV: '" + env + "'")
	}

	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	path, err = filepath.Abs(filepath.Clean(dir + path))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute config filepath: %w", err)
	}

	if _, err = os.Stat(path); err != nil {
		return nil, fmt.Errorf("stat config path: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config yaml file %s: %w", path, err)
	}

	var cfg *Cache
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal yaml from %s: %w", path, err)
	}

	for k, rule := range cfg.Cache.Rules {
		cfg.Cache.Rules[k].PathBytes = []byte(rule.Path)
		for _, param := range rule.CacheKey.Query {
			cfg.Cache.Rules[k].CacheKey.QueryBytes = append(cfg.Cache.Rules[k].CacheKey.QueryBytes, []byte(param))
		}
		for _, param := range rule.CacheKey.Headers {
			cfg.Cache.Rules[k].CacheKey.HeadersBytes = append(cfg.Cache.Rules[k].CacheKey.HeadersBytes, []byte(param))
		}
		for _, param := range rule.CacheValue.Headers {
			cfg.Cache.Rules[k].CacheValue.HeadersBytes = append(cfg.Cache.Rules[k].CacheValue.HeadersBytes, []byte(param))
		}
	}

	cfg.Cache.Refresh.MinStale = time.Duration(float64(cfg.Cache.Refresh.TTL) * cfg.Cache.Refresh.Beta)

	return cfg, nil
}
