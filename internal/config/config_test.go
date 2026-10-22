package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

const minimal = `
admin: {username: admin, password: secret}
magma: {url: "https://magma.com/api/render/x/jpg"}
`

func TestLoadDefaults(t *testing.T) {
	c, err := load(t, minimal)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.Schedule != "* * * * *" || c.Admin.SessionTTL != 7*24*time.Hour ||
		c.Magma.Timeout != 30*time.Second || c.Storage.Dir != "/data/images" || c.Storage.TempDir != "/tmp" {
		t.Fatalf("defaults: %+v", c)
	}
	if c.TrustedProxyPrefixes() != nil {
		t.Fatal("unset trusted_proxies should mean the built-in default")
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"placeholder password": `
admin: {username: admin, password: change-me}
magma: {url: "https://magma.com/x"}`,
		"bad listen": minimal + `listen: "8080"`,
		"url without host": `
admin: {username: admin, password: secret}
magma: {url: "https://"}`,
		"bad scheme": `
admin: {username: admin, password: secret}
magma: {url: "ftp://magma.com/x"}`,
		"bad proxy":    minimal + `trusted_proxies: ["nope"]`,
		"bad schedule": minimal + `schedule: "every minute"`,
	}
	for name, yaml := range cases {
		if _, err := load(t, yaml); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTrustedProxies(t *testing.T) {
	c, err := load(t, minimal+`trusted_proxies: ["127.0.0.1", "172.16.0.0/12", "::1"]`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range c.TrustedProxyPrefixes() {
		got = append(got, p.String())
	}
	if strings.Join(got, " ") != "127.0.0.1/32 172.16.0.0/12 ::1/128" {
		t.Fatalf("parsed %v", got)
	}
	c, err = load(t, minimal+`trusted_proxies: []`)
	if err != nil {
		t.Fatal(err)
	}
	if p := c.TrustedProxyPrefixes(); p == nil || len(p) != 0 {
		t.Fatalf("explicit empty list should trust nobody, got %v", p)
	}
}

func TestUnknownKeyStillLoads(t *testing.T) {
	if _, err := load(t, minimal+"lisen: \":9000\"\n"); err != nil {
		t.Fatalf("unknown key should only warn: %v", err)
	}
}

func TestNotifyConfig(t *testing.T) {
	c, err := load(t, minimal)
	if err != nil {
		t.Fatal(err)
	}
	if c.Notify.Format != "json" {
		t.Fatalf("default format %q", c.Notify.Format)
	}
	c, err = load(t, minimal+`notify: {webhook: "https://ntfy.sh/x", format: text}`)
	if err != nil || c.Notify.Format != "text" || c.Notify.Webhook != "https://ntfy.sh/x" {
		t.Fatalf("text format: %v %+v", err, c.Notify)
	}
	for name, yaml := range map[string]string{
		"non-http webhook": minimal + `notify: {webhook: "ftp://x/y"}`,
		"webhook no host":  minimal + `notify: {webhook: "https://"}`,
		"bad format":       minimal + `notify: {webhook: "https://x/y", format: xml}`,
	} {
		if _, err := load(t, yaml); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
