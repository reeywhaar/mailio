// Package config is the environment, parsed once. See docs/deploy.md.
package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"mailio/internal/seal"
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

	// SecretKeyEnv is 32 random bytes, base64, that seal users' passwords and the relay's.
	// Required.
	SecretKeyEnv = "MAILIO_SECRET_KEY"

	// MailHostEnv is the mail server's own name: what it greets other servers with, what MX
	// records point at, and the certificate's name. Unset, the host of MAILIO_PUBLIC_URL.
	MailHostEnv = "MAILIO_MAIL_HOST"

	// DNSAPIEnv is a DNS API (doapi) mailio publishes DKIM, SPF and DMARC through, and proves the
	// mail host to Let's Encrypt with. Unset, the records are for an admin to add, and the mail
	// ports keep a self-signed certificate.
	DNSAPIEnv = "MAILIO_DNS_API_URL"

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

	// MailHost is the mail server's name, lowercased, without a port.
	MailHost string

	// SecretKey seals what mailio has to read back.
	SecretKey []byte

	// DNSAPI is the DNS API's base URL, or empty.
	DNSAPI string

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
	cfg.DNSAPI = strings.TrimRight(strings.TrimSpace(os.Getenv(DNSAPIEnv)), "/")
	if cfg.SecretKey, err = parseSecretKey(os.Getenv(SecretKeyEnv)); err != nil {
		return nil, err
	}

	cfg.MailHost = strings.ToLower(strings.TrimSpace(os.Getenv(MailHostEnv)))
	if cfg.MailHost == "" {
		cfg.MailHost = public.Hostname()
	}
	if strings.ContainsAny(cfg.MailHost, "/: @") {
		return nil, fmt.Errorf("%s: %q should be a host name, e.g. mail.example.com", MailHostEnv, cfg.MailHost)
	}

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

// parseSecretKey takes either base64 alphabet, padded or not, because `openssl rand -base64`
// and a hand-copied value disagree about both.
func parseSecretKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%s is required: it seals users' passwords. Generate one with `openssl rand -base64 32` and keep it apart from the database's backups", SecretKeyEnv)
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if key, err := enc.DecodeString(raw); err == nil {
			if len(key) != seal.KeySize {
				return nil, fmt.Errorf("%s is %d bytes, and it has to be %d: generate one with `openssl rand -base64 32`", SecretKeyEnv, len(key), seal.KeySize)
			}
			return key, nil
		}
	}
	return nil, fmt.Errorf("%s is not base64: generate one with `openssl rand -base64 32`", SecretKeyEnv)
}
