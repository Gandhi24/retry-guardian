package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server ServerConfig
	Redis  RedisConfig
	Rules  RulesConfig
}

type ServerConfig struct {
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type RedisConfig struct {
	Addr         string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type RulesConfig struct {
	FilePath string `toml:"file_path"`
}

// rawConfig mirrors the TOML layout with durations as strings.
type rawConfig struct {
	Server rawServerConfig `toml:"server"`
	Redis  rawRedisConfig  `toml:"redis"`
	Rules  RulesConfig     `toml:"rules"`
}

type rawServerConfig struct {
	Port         int    `toml:"port"`
	ReadTimeout  string `toml:"read_timeout"`
	WriteTimeout string `toml:"write_timeout"`
}

type rawRedisConfig struct {
	Addr         string `toml:"addr"`
	Password     string `toml:"password"`
	DB           int    `toml:"db"`
	DialTimeout  string `toml:"dial_timeout"`
	ReadTimeout  string `toml:"read_timeout"`
	WriteTimeout string `toml:"write_timeout"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var raw rawConfig
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	serverRead, err := time.ParseDuration(raw.Server.ReadTimeout)
	if err != nil {
		return nil, fmt.Errorf("server.read_timeout: %w", err)
	}
	serverWrite, err := time.ParseDuration(raw.Server.WriteTimeout)
	if err != nil {
		return nil, fmt.Errorf("server.write_timeout: %w", err)
	}
	redisDial, err := time.ParseDuration(raw.Redis.DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("redis.dial_timeout: %w", err)
	}
	redisRead, err := time.ParseDuration(raw.Redis.ReadTimeout)
	if err != nil {
		return nil, fmt.Errorf("redis.read_timeout: %w", err)
	}
	redisWrite, err := time.ParseDuration(raw.Redis.WriteTimeout)
	if err != nil {
		return nil, fmt.Errorf("redis.write_timeout: %w", err)
	}

	cfg := &Config{
		Server: ServerConfig{
			Port:         raw.Server.Port,
			ReadTimeout:  serverRead,
			WriteTimeout: serverWrite,
		},
		Redis: RedisConfig{
			Addr:         raw.Redis.Addr,
			Password:     raw.Redis.Password,
			DB:           raw.Redis.DB,
			DialTimeout:  redisDial,
			ReadTimeout:  redisRead,
			WriteTimeout: redisWrite,
		},
		Rules: raw.Rules,
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Server.Port <= 0 {
		return fmt.Errorf("server.port must be > 0")
	}
	if c.Redis.Addr == "" {
		return fmt.Errorf("redis.addr must not be empty")
	}
	if c.Rules.FilePath == "" {
		return fmt.Errorf("rules.file_path must not be empty")
	}
	return nil
}
