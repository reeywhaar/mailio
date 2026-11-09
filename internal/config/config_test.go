package config

import (
	"net/netip"
	"testing"
)

// set clears everything this package reads, so no test passes on another's leftovers.
func set(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{PublicURLEnv, DataDirEnv, LogLevelEnv, BackupURLEnv, TrustedProxiesEnv} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestDefaults(t *testing.T) {
	set(t, map[string]string{PublicURLEnv: "https://mail.example.com/"})
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL.String() != "https://mail.example.com" || !cfg.Secure || cfg.DataDir != DefaultDataDir {
		t.Errorf("got %s, secure %v, data %s", cfg.PublicURL, cfg.Secure, cfg.DataDir)
	}
	if cfg.BackupURL != "" {
		t.Error("backups are on by default, which posts an archive somewhere nobody named")
	}
	if !cfg.TrustedProxies.Trusts(netip.MustParseAddr("172.18.0.5")) || cfg.TrustedProxies.Trusts(netip.MustParseAddr("203.0.113.7")) {
		t.Errorf("trusted proxies = %v, want the private ranges", cfg.TrustedProxies)
	}
}

func TestAPublicURLIsASchemeAndAHost(t *testing.T) {
	for _, raw := range []string{"", "mail.example.com", "ftp://mail.example.com", "https://mail.example.com/admin", "https://mail.example.com/?x=1"} {
		set(t, map[string]string{PublicURLEnv: raw})
		if _, err := Load(); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}

func TestABadSettingRefusesToStart(t *testing.T) {
	for k, v := range map[string]string{LogLevelEnv: "loud", TrustedProxiesEnv: "proxy.example.com"} {
		set(t, map[string]string{PublicURLEnv: "https://mail.example.com", k: v})
		if _, err := Load(); err == nil {
			t.Errorf("%s=%q was accepted", k, v)
		}
	}
}

func TestTrustedProxies(t *testing.T) {
	for _, c := range []struct {
		raw       string
		trusted   []string
		strangers []string
	}{
		{"private", []string{"10.1.2.3", "172.18.0.5", "192.168.1.1", "127.0.0.1", "::1", "fd00::1", "::ffff:10.1.2.3"}, []string{"203.0.113.7", "100.64.0.1", "2001:db8::1"}},
		{"none", nil, []string{"127.0.0.1", "172.18.0.5"}},
		{"198.51.100.3", []string{"198.51.100.3"}, []string{"198.51.100.4", "172.18.0.5"}},
		{"private, 198.51.100.0/24 2001:db8::/32", []string{"172.18.0.5", "198.51.100.9", "2001:db8::9"}, []string{"203.0.113.7"}},
		{"100.64.0.0/10", []string{"100.100.1.1"}, []string{"10.0.0.1"}},
	} {
		p, err := ParseProxies(c.raw)
		if err != nil {
			t.Errorf("%q: %v", c.raw, err)
			continue
		}
		for _, a := range c.trusted {
			if !p.Trusts(netip.MustParseAddr(a)) {
				t.Errorf("%q does not trust %s", c.raw, a)
			}
		}
		for _, a := range c.strangers {
			if p.Trusts(netip.MustParseAddr(a)) {
				t.Errorf("%q trusts %s", c.raw, a)
			}
		}
	}
	for _, raw := range []string{"proxy.example.com", "10.0.0.0/33", "none, 10.0.0.1"} {
		if _, err := ParseProxies(raw); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}
