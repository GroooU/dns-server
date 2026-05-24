package config

import (
	"crypto/tls"
	"strings"

	"dns-forwarder/internal/overrides"

	"github.com/spf13/viper"
)

type Config struct {
	Listen    string             `mapstructure:"listen"`
	Upstreams []string           `mapstructure:"upstreams"`
	Cache     CacheConfig        `mapstructure:"cache"`
	Metrics   MetricsConfig      `mapstructure:"metrics"`
	Log       LogConfig          `mapstructure:"log"`
	Overrides []overrides.Record `mapstructure:"overrides"`
	TLS       TLSConfig          `mapstructure:"tls"`
	DNSSEC    DNSSECConfig       `mapstructure:"dnssec"`
	RateLimit RateLimitConfig    `mapstructure:"rate_limit"`
}

type DNSSECConfig struct {
	Mode       string `mapstructure:"mode"`        // "off" | "ad" | "verify"
	BlockBogus bool   `mapstructure:"block_bogus"` // return SERVFAIL on Bogus result
}

type RateLimitConfig struct {
	Enabled        bool     `mapstructure:"enabled"`
	RequestsPerSec float64  `mapstructure:"requests_per_sec"`
	Burst          int      `mapstructure:"burst"`
	Allowlist      []string `mapstructure:"allowlist"` // CIDR ranges exempt from limiting
}

type TLSConfig struct {
	InsecureSkipVerify bool   `mapstructure:"insecure_skip_verify"`
	ServerName         string `mapstructure:"server_name"`
}

// ToStdlib returns a *tls.Config for use by upstream clients.
// Returns nil when both fields are zero-value (system defaults apply).
func (t TLSConfig) ToStdlib() *tls.Config {
	if !t.InsecureSkipVerify && t.ServerName == "" {
		return nil
	}
	return &tls.Config{
		InsecureSkipVerify: t.InsecureSkipVerify,
		ServerName:         t.ServerName,
	}
}

type MetricsConfig struct {
	Listen string `mapstructure:"listen"`
}

type CacheConfig struct {
	MaxSize int `mapstructure:"max_size"`
	MinTTL  int `mapstructure:"min_ttl"`
	MaxTTL  int `mapstructure:"max_ttl"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	setDefaults()

	// DNS_LOG_LEVEL=debug, DNS_LISTEN=0.0.0.0:53, etc.
	viper.SetEnvPrefix("DNS")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func setDefaults() {
	viper.SetDefault("listen", "0.0.0.0:53")
	viper.SetDefault("upstreams", []string{"8.8.8.8:53", "1.1.1.1:53"})
	viper.SetDefault("metrics.listen", "0.0.0.0:8053")
	viper.SetDefault("cache.max_size", 10000)
	viper.SetDefault("cache.min_ttl", 30)
	viper.SetDefault("cache.max_ttl", 3600)
	viper.SetDefault("log.level", "info")
	viper.SetDefault("tls.insecure_skip_verify", false)
	viper.SetDefault("tls.server_name", "")
	viper.SetDefault("dnssec.mode", "off")
	viper.SetDefault("dnssec.block_bogus", false)
	viper.SetDefault("rate_limit.enabled", false)
	viper.SetDefault("rate_limit.requests_per_sec", 100.0)
	viper.SetDefault("rate_limit.burst", 20)
	viper.SetDefault("rate_limit.allowlist", []string{})
}
