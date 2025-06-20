package config

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"time"
)

type V2 struct {
	Cache CacheV2 `yaml:"cache"`
}

type CacheV2 struct {
	Enabled  bool           `yaml:"enabled"`
	Eviction EvictionV2     `yaml:"eviction"`
	Refresh  RefreshV2      `yaml:"refresh"`
	Storage  StorageV2      `yaml:"storage"`
	Rules    []*CacheRuleV2 `yaml:"rules"`
}

type EvictionV2 struct {
	Policy    string  `yaml:"policy"`    // at now, it's only "lru" with works
	Threshold float64 `yaml:"threshold"` // 0.9 means 90%
}

type StorageV2 struct {
	Type string `yaml:"type"` // "malloc"
	Size uint   `yaml:"size"` // 21474836480=2gb(bytes)
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
	Beta       float64       `yaml:"beta"`      // between 0 and 1
	MinStale   time.Duration `yaml:"min_stale"` // computed=time.Duration(float64(TTL/ErrorTTL) * Beta)
	BackendURL string        `yaml:"backend_url"`
}

type CacheRuleV2 struct {
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
)

func LoadConfigV2() (*V2, error) {
	var path string

	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	if _, err = os.Stat(dir + configPathLocal); err == nil {
		path = dir + configPathLocal
	} else if os.IsNotExist(err) {
		if _, err = os.Stat(dir + configPath); err == nil {
			path = dir + configPath
		} else {
			return nil, errors.New("config does not exist by path: " + dir + configPath + " or " + dir + configPathLocal)
		}
	} else {
		return nil, fmt.Errorf("stat config path: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config yaml file %s: %w", path, err)
	}

	var cfg *V2
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
