// Package config loads and validates the YAML configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"go.yaml.in/yaml/v3"
)

type Config struct {
	Listen   string  `yaml:"listen"`
	Admin    Admin   `yaml:"admin"`
	Magma    Magma   `yaml:"magma"`
	Schedule string  `yaml:"schedule"`
	Storage  Storage `yaml:"storage"`
}

type Admin struct {
	Username      string        `yaml:"username"`
	Password      string        `yaml:"password"`
	SessionSecret string        `yaml:"session_secret"`
	SessionTTL    time.Duration `yaml:"session_ttl"`
}

type Magma struct {
	URL     string            `yaml:"url"`
	Cookie  string            `yaml:"cookie"`
	Headers map[string]string `yaml:"headers"`
	Timeout time.Duration     `yaml:"timeout"`
}

type Storage struct {
	Dir     string `yaml:"dir"`
	TempDir string `yaml:"temp_dir"`
}

// Load reads the file at path, applies defaults and validates the result.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	c := &Config{}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.Admin.SessionTTL <= 0 {
		c.Admin.SessionTTL = 7 * 24 * time.Hour
	}
	if c.Magma.Timeout <= 0 {
		c.Magma.Timeout = 30 * time.Second
	}
	if strings.TrimSpace(c.Schedule) == "" {
		c.Schedule = "* * * * *"
	}
	if c.Storage.Dir == "" {
		c.Storage.Dir = "/data/images"
	}
	if c.Storage.TempDir == "" {
		c.Storage.TempDir = "/tmp"
	}
}

func (c *Config) validate() error {
	var errs []error
	if c.Admin.Username == "" || c.Admin.Password == "" {
		errs = append(errs, errors.New("admin.username and admin.password are required"))
	}
	if c.Magma.URL == "" {
		errs = append(errs, errors.New("magma.url is required"))
	} else if !strings.HasPrefix(c.Magma.URL, "http://") && !strings.HasPrefix(c.Magma.URL, "https://") {
		errs = append(errs, errors.New("magma.url must start with http:// or https://"))
	}
	if _, err := cron.ParseStandard(c.Schedule); err != nil {
		errs = append(errs, fmt.Errorf("invalid schedule %q: %w", c.Schedule, err))
	}
	return errors.Join(errs...)
}
