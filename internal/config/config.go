// Package config loads and validates the YAML configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"go.yaml.in/yaml/v3"
)

type Config struct {
	Listen string `yaml:"listen"`
	// TrustedProxies lists the peers (IPs or CIDRs) whose X-Real-IP header is
	// believed; unset means loopback and private networks.
	TrustedProxies []string `yaml:"trusted_proxies"`
	Admin          Admin    `yaml:"admin"`
	Magma          Magma    `yaml:"magma"`
	Schedule       string   `yaml:"schedule"`
	Storage        Storage  `yaml:"storage"`

	proxies []netip.Prefix
}

// placeholderPassword is the sample config's value, which must not go live.
const placeholderPassword = "change-me"

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
	// A misspelt key would silently fall back to its default; warn rather than
	// refuse, so an upgrade never stops a running deployment over it.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&Config{}); err != nil {
		log.Printf("config: WARNING %v", err)
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
	} else if c.Admin.Password == placeholderPassword {
		errs = append(errs, fmt.Errorf("admin.password is still the sample value %q; set your own", placeholderPassword))
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		errs = append(errs, fmt.Errorf("invalid listen %q: %w", c.Listen, err))
	}
	if c.Magma.URL == "" {
		errs = append(errs, errors.New("magma.url is required"))
	} else if u, err := url.Parse(c.Magma.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, errors.New("magma.url must be an http:// or https:// URL with a host"))
	}
	for _, p := range c.TrustedProxies {
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			addr, aerr := netip.ParseAddr(p)
			if aerr != nil {
				errs = append(errs, fmt.Errorf("invalid trusted_proxies entry %q", p))
				continue
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		c.proxies = append(c.proxies, prefix.Masked())
	}
	if _, err := cron.ParseStandard(c.Schedule); err != nil {
		errs = append(errs, fmt.Errorf("invalid schedule %q: %w", c.Schedule, err))
	}
	return errors.Join(errs...)
}

// TrustedProxyPrefixes returns the parsed trusted_proxies, nil when unset.
// An explicit empty list trusts no proxy at all.
func (c *Config) TrustedProxyPrefixes() []netip.Prefix {
	if c.TrustedProxies == nil {
		return nil
	}
	return append([]netip.Prefix{}, c.proxies...)
}
