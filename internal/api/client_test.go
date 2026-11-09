package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"mailio/internal/config"
	"mailio/internal/store"
)

func TestTheCallerIsWhoTheProxySaw(t *testing.T) {
	for _, c := range []struct {
		name, proxies, peer, xff, xri, want string
	}{
		{"caddy on the docker network", "private", "172.18.0.5:41000", "203.0.113.7", "", "203.0.113.7"},
		{"only the proxy's own hop counts", "private", "172.18.0.5:41000", "198.51.100.1, 203.0.113.7", "", "203.0.113.7"},
		{"ipv6 through a proxy", "private", "172.18.0.5:41000", "2001:db8::7", "", "2001:db8::7"},
		{"loopback is a proxy too", "private", "127.0.0.1:41000", "203.0.113.7", "", "203.0.113.7"},
		{"no header, the peer", "private", "172.18.0.5:41000", "", "", "172.18.0.5"},
		{"a header that is not an address, the peer", "private", "172.18.0.5:41000", "unknown", "", "172.18.0.5"},
		{"a stranger cannot name somebody else", "private", "203.0.113.9:41000", "198.51.100.1", "", "203.0.113.9"},
		{"a stranger over ipv6", "private", "[2001:db8::9]:41000", "198.51.100.1", "", "2001:db8::9"},
		{"ipv4 mapped into ipv6", "private", "[::ffff:203.0.113.9]:41000", "", "", "203.0.113.9"},
		{"X-Real-IP is the caller's own", "private", "172.18.0.5:41000", "203.0.113.7", "198.51.100.1", "203.0.113.7"},
		{"X-Real-IP from a stranger", "private", "203.0.113.9:41000", "", "198.51.100.1", "203.0.113.9"},
		{"a proxy on another host, named", "198.51.100.3", "198.51.100.3:41000", "203.0.113.7", "", "203.0.113.7"},
		{"naming one stops trusting the rest", "198.51.100.3", "172.18.0.5:41000", "203.0.113.7", "", "172.18.0.5"},
		{"a chain of named proxies", "private, 198.51.100.0/24", "172.18.0.5:41000", "192.0.2.1, 203.0.113.7, 198.51.100.3", "", "203.0.113.7"},
		{"a chain whose first hop is not named", "private", "172.18.0.5:41000", "203.0.113.7, 198.51.100.3", "", "198.51.100.3"},
		{"nothing but proxies, the furthest", "private", "172.18.0.5:41000", "10.0.0.2, 10.0.0.3", "", "10.0.0.2"},
		{"none believes nobody", "none", "172.18.0.5:41000", "203.0.113.7", "", "172.18.0.5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := serverTrusting(t, c.proxies)
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.peer
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.xri != "" {
				r.Header.Set("X-Real-IP", c.xri)
			}
			if got := s.clientIP(r); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

// A proxy that sends the header twice means one list, in order.
func TestTwoForwardedHeadersAreOneList(t *testing.T) {
	s := serverTrusting(t, "private")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:41000"
	r.Header.Add("X-Forwarded-For", "198.51.100.1")
	r.Header.Add("X-Forwarded-For", "203.0.113.7")
	if got := s.clientIP(r); got != "203.0.113.7" {
		t.Errorf("got %s", got)
	}
}

func serverTrusting(t *testing.T, proxies string) *Server {
	t.Helper()
	p, err := config.ParseProxies(proxies)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{cfg: &config.Config{TrustedProxies: p}}
}

// failregex is the filter in docs/deploy.md, <ADDR> spelled out: the line it matches is a
// contract with a jail on the host.
var failregex = regexp.MustCompile(`^\{.*"msg":"password refused".*"ip":"([0-9A-Fa-f.:]+)"`)

func TestAWrongPasswordIsALineFail2banBansOn(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var logged bytes.Buffer
	u, _ := url.Parse("https://mail.example.com")
	proxies, _ := config.ParseProxies("private")
	s := New(&config.Config{PublicURL: u, TrustedProxies: proxies}, slog.New(slog.NewJSONHandler(&logged, nil)), st, nil)

	r := httptest.NewRequest("POST", "/api/session", strings.NewReader(`{"name":"misha","password":"guess one"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "172.18.0.5:41000"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}

	var bans []string
	for line := range strings.Lines(logged.String()) {
		if m := failregex.FindStringSubmatch(line); m != nil {
			bans = append(bans, m[1])
		}
		if strings.Contains(line, "guess one") {
			t.Errorf("the password is in the log: %s", line)
		}
	}
	if len(bans) != 1 || bans[0] != "203.0.113.7" {
		t.Errorf("fail2ban would ban %v, from:\n%s", bans, logged.String())
	}
}
