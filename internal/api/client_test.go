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

func TestTheCallerIsWhoCaddySaw(t *testing.T) {
	for _, c := range []struct {
		name, peer, xff, want string
	}{
		{"caddy on the docker network", "172.18.0.5:41000", "203.0.113.7", "203.0.113.7"},
		{"only caddy's own hop counts", "172.18.0.5:41000", "198.51.100.1, 203.0.113.7", "203.0.113.7"},
		{"ipv6 through caddy", "172.18.0.5:41000", "2001:db8::7", "2001:db8::7"},
		{"loopback is a proxy too", "127.0.0.1:41000", "203.0.113.7", "203.0.113.7"},
		{"no header, the peer", "172.18.0.5:41000", "", "172.18.0.5"},
		{"a header that is not an address, the peer", "172.18.0.5:41000", "unknown", "172.18.0.5"},
		{"a stranger cannot name somebody else", "203.0.113.9:41000", "198.51.100.1", "203.0.113.9"},
		{"a stranger over ipv6", "[2001:db8::9]:41000", "198.51.100.1", "2001:db8::9"},
		{"ipv4 mapped into ipv6", "[::ffff:203.0.113.9]:41000", "", "203.0.113.9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.peer
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := clientIP(r); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
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
	s := New(&config.Config{PublicURL: u}, slog.New(slog.NewJSONHandler(&logged, nil)), st, nil)

	r := httptest.NewRequest("POST", "/api/session", strings.NewReader(`{"address":"inbox@reey.ru","password":"guess one"}`))
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
