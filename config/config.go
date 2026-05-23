package config

import (
	"github.com/spf13/viper"
)

type Config struct {
	Listen    string      `mapstructure:"listen"`
	Upstreams []string    `mapstructure:"upstreams"`
	Cache     CacheConfig `mapstructure:"cache"`
	Log       LogConfig   `mapstructure:"log"`
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
	viper.SetDefault("cache.max_size", 10000)
	viper.SetDefault("cache.min_ttl", 30)
	viper.SetDefault("cache.max_ttl", 3600)
	viper.SetDefault("log.level", "info")
}
