// Package config loads and validates the RenderingGen worker configuration.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level worker configuration.
type Config struct {
	Worker        WorkerConfig  `yaml:"worker"`
	Queue         QueueConfig   `yaml:"queue"`
	ArtifactStore StorageConfig `yaml:"artifact_store"`
	Chronon       ChrononConfig `yaml:"chronon"`
	GPU           GPUConfig     `yaml:"gpu"`
	Health        HealthConfig  `yaml:"health"`
}

type WorkerConfig struct {
	ID string `yaml:"id"`
}

type QueueConfig struct {
	Endpoint string `yaml:"endpoint"`
}

type StorageConfig struct {
	Endpoint      string `yaml:"endpoint"`
	LocalCacheDir string `yaml:"local_cache_dir"`
}

type ChrononConfig struct {
	Backend string `yaml:"backend"`
	Home    string `yaml:"home"`
}

type GPUConfig struct {
	Device int `yaml:"device"`
}

type HealthConfig struct {
	Addr string `yaml:"addr"`
}

// Load reads the YAML config file at path, applies defaults and validates it.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(&cfg)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(c *Config) {
	if c.Worker.ID == "" {
		c.Worker.ID = "renderinggen-" + hostname()
	}
	if c.Chronon.Backend == "" {
		c.Chronon.Backend = "vulkan"
	}
	if c.Chronon.Home == "" {
		c.Chronon.Home = "/opt/chronon"
	}
	if c.ArtifactStore.LocalCacheDir == "" {
		c.ArtifactStore.LocalCacheDir = "/var/lib/renderinggen/cache"
	}
	if c.Health.Addr == "" {
		c.Health.Addr = ":8080"
	}
}

func (c *Config) validate() error {
	if c.Queue.Endpoint == "" {
		return fmt.Errorf("queue.endpoint is required")
	}
	if c.ArtifactStore.Endpoint == "" {
		return fmt.Errorf("artifact_store.endpoint is required")
	}
	return nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
