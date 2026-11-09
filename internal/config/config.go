// Package config is the environment, parsed once. See docs/deploy.md.
package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strings"
)

// Environment variables read by this package.
const (
	// PublicURLEnv is the address the admin interface is opened at. Required, and never
	// inferred: Host and X-Forwarded-Host are client-supplied, so a link built from one is a
	// link a stranger controls.
	PublicURLEnv = "MAILIO_PUBLIC_URL"

	// DataDirEnv is where mailio.db lives.
	DataDirEnv = "MAILIO_DATA_DIR"

	// LogLevelEnv sets the slog level: debug, info, warn or error.
	LogLevelEnv = "MAILIO_LOG_LEVEL"

	// BackupURLEnv is a backio agent's POST endpoint, whole address rather than a host: the path
	// is the agent's to name. Unset, nothing is backed up.
	BackupURLEnv = "MAILIO_BACKUP_URL"

	// TrustedProxiesEnv is whose X-Forwarded-For is believed: addresses and ranges, "private"
	// for the private and loopback ranges, or "none". Unset, "private".
	TrustedProxiesEnv = "MAILIO_TRUSTED_PROXIES"
)

const DefaultDataDir = "/data"

// Config is everything the process was told at startup.
type Config struct {
	// PublicURL is normalized: lowercased, no trailing slash, no path, query or fragment.
	PublicURL *url.URL

	DataDir  string
	LogLevel slog.Level

	// Secure is PublicURL being https, derived once rather than at each Set-Cookie.
	Secure bool

	// BackupURL is where an archive is posted, or empty for no backups at all.
	BackupURL string

	// TrustedProxies are the peers whose X-Forwarded-For says who the caller is. Empty, nobody's.
	TrustedProxies Proxies
}

// Proxies are the addresses a request may come through and still name its caller.
type Proxies []netip.Prefix

// Trusts reports whether addr is one of them.
func (p Proxies) Trusts(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range p {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// private is what "private" stands for: where a proxy on the same host or Docker network
// connects from.
var private = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "127.0.0.0/8", "::1/128"}

// ParseProxies reads a list of addresses, ranges, "private" and "none", split by commas or spaces.
func ParseProxies(raw string) (Proxies, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	var out Proxies
	for _, f := range fields {
		switch {
		case f == "none":
			if len(fields) > 1 {
				return nil, fmt.Errorf("%s: \"none\" goes alone, not with %q", TrustedProxiesEnv, raw)
			}
			return Proxies{}, nil
		case f == "private":
			for _, r := range private {
				out = append(out, netip.MustParsePrefix(r))
			}
		case strings.Contains(f, "/"):
			prefix, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, fmt.Errorf("%s: %q is not a range", TrustedProxiesEnv, f)
			}
			out = append(out, prefix.Masked())
		default:
			addr, err := netip.ParseAddr(f)
			if err != nil {
				return nil, fmt.Errorf("%s: %q is not an address, a range, \"private\" or \"none\"", TrustedProxiesEnv, f)
			}
			addr = addr.Unmap()
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return out, nil
}

// Link builds an absolute URL into this instance.
func (c *Config) Link(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.PublicURL.String() + path
}

// Load reads the environment. Errors are written for somebody looking at a container that
// refused to start.
func Load() (*Config, error) {
	raw := strings.TrimSpace(os.Getenv(PublicURLEnv))
	if raw == "" {
		return nil, fmt.Errorf("%s is required: set it to the address the admin interface is opened at, e.g. https://mail.example.com", PublicURLEnv)
	}
	public, err := parsePublicURL(raw)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		PublicURL: public,
		DataDir:   DefaultDataDir,
		LogLevel:  slog.LevelInfo,
		Secure:    public.Scheme == "https",
	}
	if dir := strings.TrimSpace(os.Getenv(DataDirEnv)); dir != "" {
		cfg.DataDir = dir
	}
	cfg.BackupURL = strings.TrimSpace(os.Getenv(BackupURLEnv))

	proxies := strings.TrimSpace(os.Getenv(TrustedProxiesEnv))
	if proxies == "" {
		proxies = "private"
	}
	if cfg.TrustedProxies, err = ParseProxies(proxies); err != nil {
		return nil, err
	}
	// A startup error rather than a fall back to info: a level that quietly works is one
	// nobody finds until the log lacks what they came for.
	if v := strings.TrimSpace(os.Getenv(LogLevelEnv)); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(strings.ToLower(v))); err != nil {
			return nil, fmt.Errorf("%s: %q is not a level (debug, info, warn, error)", LogLevelEnv, v)
		}
	}
	return cfg, nil
}

// parsePublicURL validates and normalizes. A trailing slash is trimmed; a path is refused,
// because mailio serves from the root and a prefix would produce links that half work.
func parsePublicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %q is not a URL: %w", PublicURLEnv, raw, err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%s: %q needs an http:// or https:// scheme", PublicURLEnv, raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%s: %q names no host", PublicURLEnv, raw)
	}
	u.Host = strings.ToLower(u.Host)
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%s: %q should be a scheme and a host, with no query or fragment", PublicURLEnv, raw)
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		return nil, fmt.Errorf("%s: %q has a path in it, and mailio serves from the root", PublicURLEnv, raw)
	}
	u.Path = ""
	u.User = nil
	return u, nil
}
