package config

import (
	"fmt"
	"github.com/brianvoe/gofakeit/v6"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const (
	Prod = "prod"
	Dev  = "dev"
	Test = "test"
)

type Config interface {
	IsProd() bool
	IsEnabled() bool
	SetEnabled(v bool)
	Runtime() *Runtime
	Api() *Api
	Upstream() *Upstream
	Data() *Data
	Refresh() *Refresh
	Eviction() *Eviction
	Storage() *Storage
	K8S() *K8S
	ForceGC() *ForceGC
	Rule(path string) (*Rule, bool)
}

func (c *Cache) IsEnabled() bool {
	return c.Cache.AtomicEnabled.Load()
}
func (c *Cache) SetEnabled(v bool) {
	c.Cache.AtomicEnabled.Store(v)
}
func (c *Cache) Runtime() *Runtime {
	return c.Cache.Runtime
}
func (c *Cache) Api() *Api {
	return c.Cache.Api
}
func (c *Cache) Upstream() *Upstream {
	return c.Cache.Upstream
}
func (c *Cache) Data() *Data {
	return c.Cache.Data
}
func (c *Cache) Refresh() *Refresh {
	return c.Cache.Refresh
}
func (c *Cache) Eviction() *Eviction {
	return c.Cache.Eviction
}
func (c *Cache) Storage() *Storage {
	return c.Cache.Storage
}
func (c *Cache) K8S() *K8S {
	return c.Cache.K8S
}
func (c *Cache) ForceGC() *ForceGC {
	return c.Cache.ForceGC
}
func (c *Cache) Rule(path string) (*Rule, bool) {
	v, ok := c.Cache.Rules[path]
	return v, ok
}

type TraefikIntermediateConfig struct {
	ConfigPath string `yaml:"configPath" mapstructure:"configPath"`
}

type Cache struct {
	Cache *CacheBox `yaml:"cache"`
}

func (c *Cache) IsProd() bool {
	return c.Cache.Env == Prod
}

func (c *Cache) IsDev() bool {
	return c.Cache.Env == Dev
}

func (c *Cache) IsTest() bool {
	return c.Cache.Env == Test
}

type Env struct {
	Value string `yaml:"value"`
}

type CacheBox struct {
	Env                 string `yaml:"env"`
	Enabled             bool   `yaml:"enabled"`
	AtomicEnabled       atomic.Bool
	CheckReloadInterval time.Duration    `yaml:"cfg_reload_check_interval"`
	Logs                *Logs            `yaml:"logs"`
	Runtime             *Runtime         `yaml:"runtime"`
	Api                 *Api             `yaml:"api"`
	Upstream            *Upstream        `yaml:"upstream"`
	Data                *Data            `yaml:"data"`
	Storage             *Storage         `yaml:"storage"`
	Eviction            *Eviction        `yaml:"eviction"`
	Refresh             *Refresh         `yaml:"refresh"`
	ForceGC             *ForceGC         `yaml:"forceGC"`
	Metrics             *Metrics         `yaml:"metrics"`
	K8S                 *K8S             `yaml:"k8s"`
	Rules               map[string]*Rule `yaml:"rules"`
}

type Api struct {
	Name string `yaml:"name"` // api server name
	Port string `yaml:"port"` // port of api server
}

type Runtime struct {
	Gomaxprocs int `yaml:"gomaxprocs"`
}

type Probe struct {
	Timeout time.Duration `yaml:"timeout"`
}

type K8S struct {
	Probe Probe `yaml:"probe"`
}

type Metrics struct {
	Enabled bool `yaml:"enabled"`
}

type Mock struct {
	Enabled bool `yaml:"enabled"`
	Length  int  `yaml:"length"`
}

type ForceGC struct {
	Enabled  bool          `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
}

type Logs struct {
	Level string `yaml:"level"` // Any zerolog.Level.
	Stats bool   `yaml:"stats"` // Should the statistic like num evictions, refreshes, rps, memory usage and so on be written in /std/out?
}

type Upstream struct {
	Policy  string   `yaml:"policy"`
	Cluster *Cluster `yaml:"cluster"`
	Backend *Backend `yaml:"backend"`
}

type Cluster struct {
	Backends []*Backend `yaml:"backends"`
}

type Backend struct {
	ID                       string        `yaml:"id"`
	Enabled                  bool          `yaml:"enabled"`
	Scheme                   string        `yaml:"scheme"` // http or https
	SchemeBytes              []byte        // virtual field, Scheme converted to []byte
	Host                     string        `yaml:"host"` // backend.example.com
	HostBytes                []byte        // virtual field, Host converted to []byte
	Rate                     int           `yaml:"rate"`                   // Rate limiting reqs to backend per second.
	Timeout                  time.Duration `yaml:"timeout"`                // Timeout for requests to backend.
	MaxTimeout               time.Duration `yaml:"max_timeout"`            // MaxTimeout for requests which are escape Timeout through UseMaxTimeoutHeaderBytes.
	UseMaxTimeoutHeader      string        `yaml:"use_max_timeout_header"` // If the header exists the timeout above will be skipped.
	UseMaxTimeoutHeaderBytes []byte        // The same value but converted into slice bytes.
	Healthcheck              string        `yaml:"healthcheck"` // Healthcheck or readiness probe path
	HealthcheckBytes         []byte        // The same value but converted into slice bytes.
}

// Clone - deep copy, obviously returns a new pointer.
func (b *Backend) Clone() *Backend {
	newb := *b

	gofakeit.FirstName()

	newSchemeBytes := make([]byte, len(b.SchemeBytes))
	copy(newSchemeBytes, b.SchemeBytes)
	newb.SchemeBytes = newSchemeBytes

	newHostBytes := make([]byte, len(b.HostBytes))
	copy(newHostBytes, b.HostBytes)
	newb.HostBytes = newHostBytes

	newUseMaxTimeoutHeaderBytes := make([]byte, len(b.UseMaxTimeoutHeaderBytes))
	copy(newUseMaxTimeoutHeaderBytes, b.UseMaxTimeoutHeaderBytes)
	newb.UseMaxTimeoutHeaderBytes = newUseMaxTimeoutHeaderBytes

	newHealthcheckBytes := make([]byte, len(b.HealthcheckBytes))
	copy(newHealthcheckBytes, b.HealthcheckBytes)
	newb.HealthcheckBytes = newHealthcheckBytes

	newb.Timeout = time.Duration(b.Timeout.Nanoseconds())
	newb.MaxTimeout = time.Duration(b.Timeout.Nanoseconds())

	return &newb
}

type Dump struct {
	IsEnabled    bool   `yaml:"enabled"`
	Dir          string `yaml:"dump_dir"`
	Name         string `yaml:"dump_name"`
	MaxVersions  int    `yaml:"max_versions"`
	Gzip         bool   `yaml:"gzip"`
	Crc32Control bool   `yaml:"crc32_control_sum"`
}

type Data struct {
	Dump *Dump `yaml:"dump"`
	Mock *Mock `yaml:"mock"`
}

type Eviction struct {
	Enabled   bool    `yaml:"enabled"`
	Threshold float64 `yaml:"threshold"` // 0.9 means 90%
}

type Storage struct {
	Size uint `yaml:"size"`
}

type Refresh struct {
	Enabled bool `yaml:"enabled"`
	// TTL - refresh TTL (max time life of response item in cache without refreshing).
	TTL      time.Duration `yaml:"ttl"`       // e.g. "1d" (responses with 200 status code)
	Rate     int           `yaml:"rate"`      // Rate limiting to external backend.
	ScanRate int           `yaml:"scan_rate"` // Rate limiting of num scans items per second.
	// beta определяет коэффициент, используемый для вычисления случайного момента обновления кэша.
	// Чем выше beta, тем чаще кэш будет обновляться до истечения TTL.
	// Формула взята из подхода "stochastic cache expiration" (см. Google Staleness paper):
	// expireTime = ttl * (-beta * ln(random()))
	// Подробнее: RFC 5861 и https://web.archive.org/web/20100829170210/http://labs.google.com/papers/staleness.pdf
	// beta: "0.4"
	Beta        float64 `yaml:"beta"`        // between 0 and 1
	Coefficient float64 `yaml:"coefficient"` // Starts attempts to renew data after TTL*coefficient=50% (12h if whole TTL is 24h)
}

type RuleRefresh struct {
	Enabled bool `yaml:"enabled"`
	// TTL - refresh TTL (max time life of response item in cache without refreshing).
	TTL time.Duration `yaml:"ttl"` // e.g. "1d" (responses with 200 status code)
	// beta определяет коэффициент, используемый для вычисления случайного момента обновления кэша.
	// Чем выше beta, тем чаще кэш будет обновляться до истечения TTL.
	// Формула взята из подхода "stochastic cache expiration" (см. Google Staleness paper):
	// expireTime = ttl * (-beta * ln(random()))
	// Подробнее: RFC 5861 и https://web.archive.org/web/20100829170210/http://labs.google.com/papers/staleness.pdf
	// beta: "0.4"
	Beta        float64 `yaml:"beta"`        // between 0 and 1
	Coefficient float64 `yaml:"coefficient"` // Starts attempts to renew data after TTL*coefficient=50% (12h if whole TTL is 24h)
}

type Rule struct {
	CacheKey   RuleKey      `yaml:"cache_key"`
	CacheValue RuleValue    `yaml:"cache_value"`
	Refresh    *RuleRefresh `yaml:"refresh"`
	PathBytes  []byte       // Virtual field
}

type RuleKey struct {
	Query      []string          `yaml:"query"` // Параметры, которые будут участвовать в ключе кэширования
	QueryBytes [][]byte          // Virtual field
	Headers    []string          `yaml:"headers"` // Хедеры, которые будут участвовать в ключе кэширования
	HeadersMap map[string][]byte // Virtual field
}

type RuleValue struct {
	Headers    []string            `yaml:"headers"` // Хедеры ответа, которые будут сохранены в кэше вместе с body
	HeadersMap map[string]struct{} // Virtual field
}

func LoadConfig(path string) (*Cache, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	path, err = filepath.Abs(filepath.Clean(filepath.Join(dir, path)))
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

	cfg.Cache.AtomicEnabled.Store(cfg.Cache.Enabled)

	for rulePath, rule := range cfg.Cache.Rules {
		rule.PathBytes = []byte(rulePath)

		// Query
		for _, query := range rule.CacheKey.Query {
			rule.CacheKey.QueryBytes = append(rule.CacheKey.QueryBytes, []byte(query))
		}

		// Request headers
		keyHeadersMap := make(map[string][]byte, len(rule.CacheKey.Headers))
		for _, header := range rule.CacheKey.Headers {
			keyHeadersMap[header] = []byte(header)
		}
		rule.CacheKey.HeadersMap = keyHeadersMap

		// Response headers
		valueHeadersMap := make(map[string]struct{}, len(rule.CacheValue.Headers))
		for _, header := range rule.CacheValue.Headers {
			valueHeadersMap[header] = struct{}{}
		}
		rule.CacheValue.HeadersMap = valueHeadersMap
	}

	if cfg.Cache.Upstream.Cluster != nil {
		for i, _ := range cfg.Cache.Upstream.Cluster.Backends {
			cfg.Cache.Upstream.Cluster.Backends[i].SchemeBytes = []byte(cfg.Cache.Upstream.Cluster.Backends[i].Scheme)
			cfg.Cache.Upstream.Cluster.Backends[i].HostBytes = []byte(cfg.Cache.Upstream.Cluster.Backends[i].Host)
			cfg.Cache.Upstream.Cluster.Backends[i].UseMaxTimeoutHeaderBytes = []byte(cfg.Cache.Upstream.Cluster.Backends[i].UseMaxTimeoutHeader)
			cfg.Cache.Upstream.Cluster.Backends[i].HealthcheckBytes = []byte(cfg.Cache.Upstream.Cluster.Backends[i].Healthcheck)
		}
	} else if cfg.Cache.Upstream.Backend != nil {
		cfg.Cache.Upstream.Backend.SchemeBytes = []byte(cfg.Cache.Upstream.Backend.Scheme)
		cfg.Cache.Upstream.Backend.HostBytes = []byte(cfg.Cache.Upstream.Backend.Host)
		cfg.Cache.Upstream.Backend.UseMaxTimeoutHeaderBytes = []byte(cfg.Cache.Upstream.Backend.UseMaxTimeoutHeader)
		cfg.Cache.Upstream.Backend.HealthcheckBytes = []byte(cfg.Cache.Upstream.Backend.Healthcheck)
	} else {
		return nil, fmt.Errorf("no backend configured")
	}

	return cfg, nil
}
