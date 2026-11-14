package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"mailio/internal/config"
	"mailio/internal/dnscheck"
	"mailio/internal/seal"
	"mailio/internal/store"
)

func TestMain(m *testing.M) {
	store.SetBcryptCost(bcrypt.MinCost)
	os.Exit(m.Run())
}

type harness struct {
	t   *testing.T
	st  *store.Store
	srv *httptest.Server
	c   *http.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, nil)
}

// newHarnessWith runs the server with the DNS dns makes, nil for none.
func newHarnessWith(t *testing.T, dns func(*store.Store) DNS) *harness {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, _ := seal.New(make([]byte, seal.KeySize))
	st.SetSealer(sealer)
	u, _ := url.Parse("http://mail.example.com")
	cfg := &config.Config{PublicURL: u, MailHost: "mail.example.com"}
	var d DNS
	if dns != nil {
		d = dns(st)
	}
	srv := httptest.NewServer(New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, nil, d, nil))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &harness{t: t, st: st, srv: srv, c: &http.Client{Jar: jar}}
}

// fresh is another browser: the same server, no cookies.
func (h *harness) fresh() *harness {
	jar, _ := cookiejar.New(nil)
	return &harness{t: h.t, st: h.st, srv: h.srv, c: &http.Client{Jar: jar}}
}

func (h *harness) do(method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 && raw[0] == '{' {
		json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

func (h *harness) list(path string) []map[string]any {
	h.t.Helper()
	resp, err := h.c.Get(h.srv.URL + path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("GET %s: %s", path, resp.Status)
	}
	var out []map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// signedIn is a browser that has used the first admin link.
func signedIn(t *testing.T) *harness {
	t.Helper()
	return signIn(t, newHarness(t))
}

func signIn(t *testing.T, h *harness) *harness {
	t.Helper()
	token, _, err := h.st.CreateAdminLink(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code, body := h.do("POST", "/api/admin-link", map[string]any{"token": token, "name": "misha", "password": "correct horse"}); code != http.StatusNoContent {
		t.Fatalf("admin link: %d %v", code, body)
	}
	return h
}

func TestAnAdminLinkIsCheckedThenSpentAndSignsIn(t *testing.T) {
	h := newHarness(t)
	token, _, _ := h.st.CreateAdminLink(context.Background())

	if code, body := h.do("POST", "/api/admin-link/check", map[string]any{"token": token}); code != http.StatusOK || body["expires_at"] == nil {
		t.Fatalf("check: %d %v", code, body)
	}
	if code, _ := h.do("GET", "/api/me", nil); code != http.StatusUnauthorized {
		t.Fatalf("me before: %d", code)
	}
	if code, body := h.do("POST", "/api/admin-link", map[string]any{"token": token, "name": "Misha", "password": "correct horse"}); code != http.StatusNoContent {
		t.Fatalf("use: %d %v", code, body)
	}
	code, me := h.do("GET", "/api/me", nil)
	if code != http.StatusOK || me["name"] != "misha" {
		t.Fatalf("me after: %d %v", code, me)
	}
	// An admin is not a mailbox, so the link made no domain and no account.
	if len(h.list("/api/domains")) != 0 || len(h.list("/api/accounts")) != 0 {
		t.Error("the admin link made a domain or an account")
	}
	if code, _ := h.do("POST", "/api/admin-link/check", map[string]any{"token": token}); code != http.StatusNotFound {
		t.Errorf("a spent link checks as %d", code)
	}
}

func TestAnAccountCannotSignInHereOnlyAnAdmin(t *testing.T) {
	h := signedIn(t)
	if code, body := h.do("POST", "/api/domains", map[string]any{"name": "example.org"}); code != http.StatusCreated {
		t.Fatalf("domain: %d %v", code, body)
	}
	if code, body := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"}); code != http.StatusCreated {
		t.Fatalf("account: %d %v", code, body)
	}

	b := h.fresh()
	if code, _ := b.do("POST", "/api/session", map[string]any{"name": "inbox@example.org", "password": "correct horse"}); code != http.StatusUnauthorized {
		t.Errorf("an account: %d", code)
	}
	if code, _ := b.do("POST", "/api/session", map[string]any{"name": "misha", "password": "wrong password"}); code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", code)
	}
	if code, _ := b.do("POST", "/api/session", map[string]any{"name": "Misha", "password": "correct horse"}); code != http.StatusNoContent {
		t.Errorf("an admin: %d", code)
	}
	if code, _ := b.do("GET", "/api/me", nil); code != http.StatusOK {
		t.Errorf("me: %d", code)
	}
}

func TestSigningOutEndsTheSession(t *testing.T) {
	h := signedIn(t)
	if code, _ := h.do("DELETE", "/api/session", nil); code != http.StatusNoContent {
		t.Fatalf("sign out: %d", code)
	}
	if code, _ := h.do("GET", "/api/me", nil); code != http.StatusUnauthorized {
		t.Errorf("me after sign out: %d", code)
	}
}

func TestEveryAdminRouteNeedsASession(t *testing.T) {
	h := newHarness(t)
	for _, c := range [][2]string{
		{"GET", "/api/domains"}, {"POST", "/api/domains"}, {"PATCH", "/api/domains/dm_x"}, {"DELETE", "/api/domains/dm_x"},
		{"GET", "/api/accounts"}, {"POST", "/api/accounts"}, {"PATCH", "/api/accounts/ac_x"}, {"DELETE", "/api/accounts/ac_x"},
		{"DELETE", "/api/accounts/ac_x/mail"}, {"DELETE", "/api/queue/1"},
		{"POST", "/api/accounts/ac_x/credentials"}, {"DELETE", "/api/accounts/ac_x/credentials/cr_x"},
		{"POST", "/api/accounts/ac_x/credentials/cr_x/reset"},
		{"GET", "/api/admins"}, {"POST", "/api/admins"}, {"PATCH", "/api/admins/ad_x"}, {"DELETE", "/api/admins/ad_x"},
		{"GET", "/api/relays"}, {"POST", "/api/relays"}, {"PUT", "/api/relays/rl_x"}, {"DELETE", "/api/relays/rl_x"},
		{"POST", "/api/relays/rl_x/test"}, {"GET", "/api/settings"}, {"PUT", "/api/settings"}, {"GET", "/api/queue"},
		{"GET", "/api/me"}, {"DELETE", "/api/session"},
	} {
		if code, _ := h.do(c[0], c[1], map[string]any{}); code != http.StatusUnauthorized {
			t.Errorf("%s %s: %d", c[0], c[1], code)
		}
	}
}

func TestAnAdminManagesDomainsAndAccounts(t *testing.T) {
	h := signedIn(t)

	code, d := h.do("POST", "/api/domains", map[string]any{"name": "Example.NET"})
	if code != http.StatusCreated || d["name"] != "example.net" || d["unknown"] != nil {
		t.Fatalf("create domain: %d %v", code, d)
	}
	code, u := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.net"})
	if code != http.StatusCreated || u["address"] != "inbox@example.net" {
		t.Fatalf("create account: %d %v", code, u)
	}

	domainID, accountID := d["id"].(string), u["id"].(string)
	code, d = h.do("PATCH", "/api/domains/"+domainID, map[string]any{"unknown": "junk", "unknown_account_id": accountID})
	if code != http.StatusOK || d["unknown"] != "junk" || d["unknown_account_id"] != accountID {
		t.Fatalf("set junk: %d %v", code, d)
	}
	if code, _ := h.do("DELETE", "/api/accounts/"+accountID, nil); code != http.StatusConflict {
		t.Errorf("deleting the junk account: %d", code)
	}
	if code, _ := h.do("PATCH", "/api/domains/"+domainID, map[string]any{"unknown": "drop"}); code != http.StatusOK {
		t.Errorf("set drop: %d", code)
	}

	if len(h.list("/api/accounts")) != 1 || len(h.list("/api/domains")) != 1 {
		t.Error("the lists do not have what was made")
	}

	if code, _ := h.do("DELETE", "/api/accounts/"+accountID, nil); code != http.StatusNoContent {
		t.Errorf("delete account: %d", code)
	}
	if code, _ := h.do("DELETE", "/api/domains/"+domainID, nil); code != http.StatusNoContent {
		t.Errorf("delete domain: %d", code)
	}
}

func TestAnAdminManagesAdmins(t *testing.T) {
	h := signedIn(t)
	code, a := h.do("POST", "/api/admins", map[string]any{"name": "anna", "password": "correct horse"})
	if code != http.StatusCreated || a["name"] != "anna" {
		t.Fatalf("create: %d %v", code, a)
	}
	if code, _ := h.do("POST", "/api/admins", map[string]any{"name": "ANNA", "password": "correct horse"}); code != http.StatusConflict {
		t.Errorf("a name taken: %d", code)
	}
	if len(h.list("/api/admins")) != 2 {
		t.Error("the list does not have both")
	}

	// Anna signs in elsewhere, and a new password ends that session.
	anna := h.fresh()
	if code, _ := anna.do("POST", "/api/session", map[string]any{"name": "anna", "password": "correct horse"}); code != http.StatusNoContent {
		t.Fatalf("anna signs in: %d", code)
	}
	id := a["id"].(string)
	if code, _ := h.do("PATCH", "/api/admins/"+id, map[string]any{"password": "a new password"}); code != http.StatusOK {
		t.Fatalf("password: %d", code)
	}
	if code, _ := anna.do("GET", "/api/me", nil); code != http.StatusUnauthorized {
		t.Errorf("anna's session outlived her password: %d", code)
	}

	if code, _ := h.do("DELETE", "/api/admins/"+id, nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	_, me := h.do("GET", "/api/me", nil)
	if code, _ := h.do("DELETE", "/api/admins/"+me["id"].(string), nil); code != http.StatusConflict {
		t.Errorf("deleting the last admin: %d", code)
	}
}

// Changing your own password keeps you signed in here, and only here.
func TestYourOwnNewPasswordKeepsThisSession(t *testing.T) {
	h := signedIn(t)
	elsewhere := h.fresh()
	if code, _ := elsewhere.do("POST", "/api/session", map[string]any{"name": "misha", "password": "correct horse"}); code != http.StatusNoContent {
		t.Fatalf("sign in elsewhere: %d", code)
	}
	_, me := h.do("GET", "/api/me", nil)
	if code, _ := h.do("PATCH", "/api/admins/"+me["id"].(string), map[string]any{"password": "a new password"}); code != http.StatusOK {
		t.Fatalf("password: %d", code)
	}
	if code, _ := h.do("GET", "/api/me", nil); code != http.StatusOK {
		t.Errorf("signed out here: %d", code)
	}
	if code, _ := elsewhere.do("GET", "/api/me", nil); code != http.StatusUnauthorized {
		t.Errorf("still signed in elsewhere: %d", code)
	}
}

func TestAMalformedIdIsABadRequest(t *testing.T) {
	h := signedIn(t)
	if code, _ := h.do("DELETE", "/api/accounts/nope", nil); code != http.StatusBadRequest {
		t.Errorf("got %d", code)
	}
}

func TestABodyMustBeJSON(t *testing.T) {
	h := signedIn(t)
	resp, err := h.c.Post(h.srv.URL+"/api/domains", "application/x-www-form-urlencoded", strings.NewReader("name=x.com"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("got %s", resp.Status)
	}
}

func TestAnUnknownAPIPathIsJSON(t *testing.T) {
	h := newHarness(t)
	code, body := h.do("GET", "/api/nope", nil)
	if code != http.StatusNotFound || body["code"] != CodeNotFound {
		t.Errorf("got %d %v", code, body)
	}
}

func TestEveryOtherPathIsTheApp(t *testing.T) {
	h := newHarness(t)
	resp, err := h.c.Get(h.srv.URL + "/admin/password")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("got %s %s", resp.Status, resp.Header.Get("Content-Type"))
	}
}

func TestAnAdminLinkSendsNoServerItsToken(t *testing.T) {
	u, err := url.Parse("https://mail.example.com" + AdminLinkPath("s3cret-_token"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/admin/password" || u.RawQuery != "" {
		t.Errorf("a server would see %s?%s", u.Path, u.RawQuery)
	}
	if got, _ := url.ParseQuery(u.Fragment); got.Get("token") != "s3cret-_token" {
		t.Errorf("fragment %q", u.Fragment)
	}
}

func TestRelaysAreListedWithoutTheirPasswords(t *testing.T) {
	h := signedIn(t)
	code, r := h.do("POST", "/api/relays", map[string]any{"label": "Gmail", "host": "smtp.gmail.com", "port": 587, "security": "starttls", "username": "me", "password": "relay secret"})
	if code != http.StatusCreated || r["has_password"] != true || r["password"] != nil {
		t.Fatalf("add: %d %v", code, r)
	}
	id := r["id"].(string)
	if code, _ := h.do("POST", "/api/relays", map[string]any{"label": "GMAIL", "host": "x.example", "port": 25, "security": "starttls"}); code != http.StatusConflict {
		t.Errorf("a taken label: %d", code)
	}
	// A new port keeps the password the form never showed.
	code, r = h.do("PUT", "/api/relays/"+id, map[string]any{"label": "Gmail", "host": "smtp.gmail.com", "port": 465, "security": "implicit", "username": "me", "password": ""})
	if code != http.StatusOK || r["has_password"] != true || r["port"] != float64(465) {
		t.Fatalf("change port: %d %v", code, r)
	}
	if stored, _ := h.st.RelayByID(context.Background(), id); stored.Password != "relay secret" {
		t.Errorf("the password became %q", stored.Password)
	}
	if list := h.list("/api/relays"); len(list) != 1 || strings.Contains(fmt.Sprint(list), "relay secret") {
		t.Errorf("list: %v", list)
	}
	if code, _ := h.do("PUT", "/api/relays/"+id, map[string]any{"label": "Gmail", "host": "smtp.gmail.com", "port": 0, "security": "starttls"}); code != http.StatusBadRequest {
		t.Errorf("port 0: %d", code)
	}
	if code, _ := h.do("DELETE", "/api/relays/"+id, nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
}

func TestAnAccountHasCredentialsEachWithItsLogin(t *testing.T) {
	h := signedIn(t)
	h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	_, u := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"})
	id := u["id"].(string)
	// The default is generated, and this answer is the one time it is shown.
	generated, _ := u["password"].(string)
	if len(generated) != store.GeneratedLength {
		t.Fatalf("generated %q", generated)
	}
	if _, err := h.st.Authenticate(context.Background(), "inbox@example.org", generated); err != nil {
		t.Errorf("the generated default does not sign in: %v", err)
	}
	if len(h.list("/api/accounts")[0]) == 0 || h.list("/api/accounts")[0]["password"] != nil {
		t.Error("the list carries a password")
	}
	list := u["credentials"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["label"] != "default" || list[0].(map[string]any)["login"] != "inbox@example.org" {
		t.Fatalf("a new account's credentials: %v", list)
	}

	code, p := h.do("POST", "/api/accounts/"+id+"/credentials", map[string]any{"label": "iPhone"})
	if code != http.StatusCreated || p["label"] != "iPhone" {
		t.Fatalf("add: %d %v", code, p)
	}
	phone := p["password"].(string)
	login := p["login"].(string)
	if !strings.HasPrefix(login, "inbox+") || !strings.HasSuffix(login, "@example.org") {
		t.Errorf("login %q", login)
	}
	if _, err := h.st.Authenticate(context.Background(), login, phone); err != nil {
		t.Errorf("the new login does not sign in: %v", err)
	}
	pid := p["id"].(string)

	// A new password for the same login: the old one stops.
	code, p = h.do("POST", "/api/accounts/"+id+"/credentials/"+pid+"/reset", map[string]any{"password": ""})
	if code != http.StatusOK || p["login"] != login || p["password"] == phone || len(p["password"].(string)) != store.GeneratedLength {
		t.Fatalf("reset: %d %v", code, p)
	}
	if _, err := h.st.Authenticate(context.Background(), login, phone); err == nil {
		t.Error("the old password still signs in")
	}

	// One the admin types, for a mail client particular about passwords, and never one too short.
	if code, _ := h.do("POST", "/api/accounts/"+id+"/credentials/"+pid+"/reset", map[string]any{"password": "short"}); code != http.StatusBadRequest {
		t.Errorf("a short password: %d", code)
	}
	code, p = h.do("POST", "/api/accounts/"+id+"/credentials/"+pid+"/reset", map[string]any{"password": "only letters here"})
	if code != http.StatusOK || p["password"] != "only letters here" {
		t.Fatalf("a typed password: %d %v", code, p)
	}
	phone = p["password"].(string)
	if _, err := h.st.Authenticate(context.Background(), login, phone); err != nil {
		t.Errorf("the typed password does not sign in: %v", err)
	}
	code, typed := h.do("POST", "/api/accounts/"+id+"/credentials", map[string]any{"label": "Old client", "password": "typed for it"})
	if code != http.StatusCreated || typed["password"] != "typed for it" {
		t.Errorf("a credential added with a typed password: %d %v", code, typed)
	}
	if code, _ := h.do("DELETE", "/api/accounts/"+id+"/credentials/"+pid, nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if _, err := h.st.Authenticate(context.Background(), login, phone); err == nil {
		t.Error("a deleted password still signs in")
	}
	if code, _ := h.do("DELETE", "/api/accounts/"+id+"/credentials/"+pid, nil); code != http.StatusNotFound {
		t.Errorf("deleting it twice: %d", code)
	}
}

func TestAnAccountRefusesOrForwardsItsMail(t *testing.T) {
	h := signedIn(t)
	h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	_, inbox := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"})
	_, billing := h.do("POST", "/api/accounts", map[string]any{"address": "billing@example.org"})
	if billing["incoming"] != "keep" || billing["forward_account_id"] != nil {
		t.Errorf("a new account: %v", billing)
	}
	code, u := h.do("PATCH", "/api/accounts/"+billing["id"].(string), map[string]any{"incoming": "forward", "forward_account_id": inbox["id"]})
	if code != http.StatusOK || u["incoming"] != "forward" || u["forward_account_id"] != inbox["id"] {
		t.Errorf("forward: %d %v", code, u)
	}
	code, u = h.do("PATCH", "/api/accounts/"+billing["id"].(string), map[string]any{"incoming": "reject"})
	if code != http.StatusOK || u["incoming"] != "reject" || u["forward_account_id"] != nil {
		t.Errorf("reject: %d %v", code, u)
	}
	if code, _ := h.do("PATCH", "/api/accounts/"+billing["id"].(string), map[string]any{"incoming": "forward"}); code != http.StatusBadRequest {
		t.Errorf("forward to nobody: %d", code)
	}
}

func TestTheDefaultsAndADomainsOwn(t *testing.T) {
	h := signedIn(t)
	if code, set := h.do("GET", "/api/settings", nil); code != http.StatusOK || set["dmarc"] != "reject" || set["spf"] != "~all" || set["relay_id"] != nil {
		t.Fatalf("defaults: %d %v", code, set)
	}
	_, gmail := h.do("POST", "/api/relays", map[string]any{"label": "Gmail", "host": "smtp.gmail.com", "port": 587, "security": "starttls", "username": "me", "password": "relay secret"})
	code, set := h.do("PUT", "/api/settings", map[string]any{"unknown": "reject", "dmarc": "quarantine", "spf": "-all", "relay_id": gmail["id"]})
	if code != http.StatusOK || set["spf"] != "-all" || set["relay_id"] != gmail["id"] {
		t.Fatalf("set defaults: %d %v", code, set)
	}
	_, d := h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	id := d["id"].(string)
	if d["dmarc"] != nil || d["spf"] != nil || d["relay"] != nil {
		t.Errorf("a new domain: %v", d)
	}
	code, d = h.do("PATCH", "/api/domains/"+id, map[string]any{"dmarc": "none", "relay": "builtin"})
	if code != http.StatusOK || d["dmarc"] != "none" || d["relay"] != "builtin" {
		t.Errorf("its own: %d %v", code, d)
	}
	code, d = h.do("PATCH", "/api/domains/"+id, map[string]any{"relay": gmail["id"]})
	if code != http.StatusOK || d["relay"] != gmail["id"] {
		t.Errorf("a relay of its own: %d %v", code, d)
	}
	code, d = h.do("PATCH", "/api/domains/"+id, map[string]any{"relay": "", "dmarc": ""})
	if code != http.StatusOK || d["relay"] != nil || d["dmarc"] != nil {
		t.Errorf("back to the defaults: %d %v", code, d)
	}
}

func TestTheLargestMessageIsTheDefaultOrADomainsOrAnAccountsOwn(t *testing.T) {
	h := signedIn(t)
	const mb = 1 << 20
	_, set := h.do("GET", "/api/settings", nil)
	if set["max_message_bytes"] != float64(50*mb) {
		t.Errorf("the default: %v", set["max_message_bytes"])
	}
	// Left out, it stays.
	_, set = h.do("PUT", "/api/settings", map[string]any{"unknown": "drop", "dmarc": "reject", "spf": "~all"})
	if set["unknown"] != "drop" || set["max_message_bytes"] != float64(50*mb) {
		t.Errorf("left out: %v", set)
	}
	code, set := h.do("PUT", "/api/settings", map[string]any{"unknown": "drop", "dmarc": "reject", "spf": "~all", "max_message_bytes": 30 * mb})
	if code != http.StatusOK || set["max_message_bytes"] != float64(30*mb) {
		t.Errorf("set: %d %v", code, set)
	}

	_, d := h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	id := d["id"].(string)
	if d["max_message_bytes"] != nil {
		t.Errorf("a new domain: %v", d["max_message_bytes"])
	}
	code, d = h.do("PATCH", "/api/domains/"+id, map[string]any{"max_message_bytes": 20 * mb})
	if code != http.StatusOK || d["max_message_bytes"] != float64(20*mb) {
		t.Errorf("its own: %d %v", code, d["max_message_bytes"])
	}
	code, d = h.do("PATCH", "/api/domains/"+id, map[string]any{"max_message_bytes": 0})
	if code != http.StatusOK || d["max_message_bytes"] != nil {
		t.Errorf("back to the default: %d %v", code, d["max_message_bytes"])
	}

	_, a := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"})
	code, a = h.do("PATCH", "/api/accounts/"+a["id"].(string), map[string]any{"max_message_bytes": 10 * mb})
	if code != http.StatusOK || a["max_message_bytes"] != float64(10*mb) || a["incoming"] != "keep" {
		t.Errorf("an account's own, its incoming untouched: %d %v", code, a)
	}
	if code, _ := h.do("PATCH", "/api/accounts/"+a["id"].(string), map[string]any{"max_message_bytes": 1000}); code != http.StatusBadRequest {
		t.Errorf("1000 bytes: %d", code)
	}
}

func TestAnAccountSendsThroughItsOwnRelay(t *testing.T) {
	h := signedIn(t)
	h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	_, a := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"})
	id := a["id"].(string)
	if a["relay"] != nil {
		t.Errorf("a new account: %v", a["relay"])
	}
	_, gmail := h.do("POST", "/api/relays", map[string]any{"label": "Gmail", "host": "smtp.gmail.com", "port": 587, "security": "starttls"})
	code, a := h.do("PATCH", "/api/accounts/"+id, map[string]any{"relay": gmail["id"]})
	if code != http.StatusOK || a["relay"] != gmail["id"] || a["incoming"] != "keep" {
		t.Errorf("its own relay: %d %v", code, a)
	}
	if code, _ := h.do("DELETE", "/api/relays/"+gmail["id"].(string), nil); code != http.StatusConflict {
		t.Errorf("deleting the relay it uses: %d", code)
	}
	code, a = h.do("PATCH", "/api/accounts/"+id, map[string]any{"relay": "builtin"})
	if code != http.StatusOK || a["relay"] != "builtin" {
		t.Errorf("built-in: %d %v", code, a["relay"])
	}
	code, a = h.do("PATCH", "/api/accounts/"+id, map[string]any{"relay": ""})
	if code != http.StatusOK || a["relay"] != nil {
		t.Errorf("back to its domain's: %d %v", code, a["relay"])
	}
}

func TestAnAccountSendsAsAnother(t *testing.T) {
	h := signedIn(t)
	h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	_, info := h.do("POST", "/api/accounts", map[string]any{"address": "info@example.org"})
	_, inbox := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"})
	id := info["id"].(string)
	if info["send_as_account_id"] != nil {
		t.Errorf("a new account: %v", info["send_as_account_id"])
	}
	code, a := h.do("PATCH", "/api/accounts/"+id, map[string]any{"relay": "builtin"})
	if code != http.StatusOK || a["relay"] != "builtin" {
		t.Fatalf("built-in: %d %v", code, a)
	}
	code, a = h.do("PATCH", "/api/accounts/"+id, map[string]any{"send_as_account_id": inbox["id"]})
	if code != http.StatusOK || a["send_as_account_id"] != inbox["id"] || a["relay"] != nil {
		t.Errorf("as inbox: %d %v", code, a)
	}
	if code, _ := h.do("DELETE", "/api/accounts/"+inbox["id"].(string), nil); code != http.StatusConflict {
		t.Errorf("deleting the account sent as: %d", code)
	}
	if code, _ := h.do("PATCH", "/api/accounts/"+id, map[string]any{"relay": "builtin", "send_as_account_id": inbox["id"]}); code != http.StatusBadRequest {
		t.Errorf("a relay and an account: %d", code)
	}
	code, a = h.do("PATCH", "/api/accounts/"+id, map[string]any{"relay": "", "send_as_account_id": ""})
	if code != http.StatusOK || a["send_as_account_id"] != nil || a["relay"] != nil {
		t.Errorf("as itself again: %d %v", code, a)
	}
}

func TestAnAliasIsMadeOfAnAccountAndHasNoPassword(t *testing.T) {
	h := signedIn(t)
	h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	_, inbox := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"})
	if inbox["kind"] != "account" {
		t.Errorf("an account: %v", inbox["kind"])
	}
	code, a := h.do("POST", "/api/accounts", map[string]any{"address": "shop@example.org", "alias_of": inbox["id"]})
	if code != http.StatusCreated || a["kind"] != "alias" || a["forward_account_id"] != inbox["id"] || a["password"] != nil {
		t.Fatalf("an alias: %d %v", code, a)
	}
	if code, _ := h.do("POST", "/api/accounts/"+a["id"].(string)+"/credentials", map[string]any{"label": "phone"}); code != http.StatusBadRequest {
		t.Errorf("a credential for it: %d", code)
	}
	if code, _ := h.do("DELETE", "/api/accounts/"+inbox["id"].(string), nil); code != http.StatusConflict {
		t.Errorf("deleting its account: %d", code)
	}
}

func TestTrashDaysAreTheDefaultOrADomainsOrAnAccountsOwn(t *testing.T) {
	h := signedIn(t)
	if _, set := h.do("GET", "/api/settings", nil); set["trash_days"] != float64(30) {
		t.Errorf("the default: %v", set["trash_days"])
	}
	_, set := h.do("PUT", "/api/settings", map[string]any{"unknown": "reject", "dmarc": "reject", "spf": "~all", "trash_days": 14})
	if set["trash_days"] != float64(14) {
		t.Errorf("set: %v", set["trash_days"])
	}
	_, d := h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	id := d["id"].(string)
	// 0 is forever, its own; null goes back to the default.
	if code, d := h.do("PATCH", "/api/domains/"+id, map[string]any{"trash_days": 0}); code != http.StatusOK || d["trash_days"] != float64(0) {
		t.Errorf("forever: %d %v", code, d["trash_days"])
	}
	if code, d := h.do("PATCH", "/api/domains/"+id, map[string]any{"dmarc": "none"}); code != http.StatusOK || d["trash_days"] != float64(0) {
		t.Errorf("left out: %d %v", code, d["trash_days"])
	}
	if code, d := h.do("PATCH", "/api/domains/"+id, map[string]any{"trash_days": nil}); code != http.StatusOK || d["trash_days"] != nil {
		t.Errorf("back to the default: %d %v", code, d["trash_days"])
	}
	_, a := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"})
	if code, a := h.do("PATCH", "/api/accounts/"+a["id"].(string), map[string]any{"trash_days": 7}); code != http.StatusOK || a["trash_days"] != float64(7) {
		t.Errorf("an account's own: %d %v", code, a["trash_days"])
	}
	if code, _ := h.do("PATCH", "/api/accounts/"+a["id"].(string), map[string]any{"trash_days": -1}); code != http.StatusBadRequest {
		t.Errorf("-1: %d", code)
	}
	// Junk the same, apart.
	if _, set := h.do("GET", "/api/settings", nil); set["junk_days"] != float64(30) {
		t.Errorf("junk, the default: %v", set["junk_days"])
	}
	if code, d := h.do("PATCH", "/api/domains/"+id, map[string]any{"junk_days": 3}); code != http.StatusOK || d["junk_days"] != float64(3) || d["trash_days"] != nil {
		t.Errorf("junk, its own: %d %v", code, d)
	}
	if code, a := h.do("PATCH", "/api/accounts/"+a["id"].(string), map[string]any{"junk_days": 0}); code != http.StatusOK || a["junk_days"] != float64(0) || a["trash_days"] != float64(7) {
		t.Errorf("junk, an account's own: %d %v", code, a)
	}
}

func TestAnAccountShowsItsMessagesAndHasThemCleared(t *testing.T) {
	h := signedIn(t)
	ctx := context.Background()
	h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	_, a := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"})
	id := a["id"].(string)
	if a["messages"] != float64(0) {
		t.Errorf("a new account: %v", a["messages"])
	}
	box, _ := h.st.MailboxNamed(ctx, id, store.Inbox)
	for range 2 {
		if err := h.st.Deliver(ctx, []byte("Subject: hi\r\n\r\nbody\r\n"), time.Now(), []store.Delivery{{MailboxID: box.ID}}); err != nil {
			t.Fatal(err)
		}
	}
	list := h.list("/api/accounts")
	if len(list) != 1 || list[0]["messages"] != float64(2) || list[0]["bytes"] != float64(2*len("Subject: hi\r\n\r\nbody\r\n")) {
		t.Errorf("listed: %v", list)
	}
	code, a := h.do("DELETE", "/api/accounts/"+id+"/mail", nil)
	if code != http.StatusOK || a["messages"] != float64(0) || a["bytes"] != float64(0) {
		t.Errorf("cleared: %d %v", code, a)
	}
	if _, err := h.st.MailboxNamed(ctx, id, store.Inbox); err != nil {
		t.Errorf("its INBOX went too: %v", err)
	}
}

func TestAHeldMessageIsCancelledFromTheQueue(t *testing.T) {
	h := signedIn(t)
	ctx := context.Background()
	h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	_, a := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@example.org"})
	held := store.Held{Sender: "inbox@example.org", AccountID: a["id"].(string), Raw: []byte("Subject: later\r\n\r\nbody\r\n"),
		Recipients: []string{"someone@example.com"}, Release: time.Now().Add(time.Hour), Copy: []byte("Subject: later\r\n\r\nbody\r\n")}
	if err := h.st.Hold(ctx, held); err != nil {
		t.Fatal(err)
	}
	if err := h.st.Enqueue(ctx, "inbox@example.org", a["id"].(string), []byte("Subject: now\r\n\r\nbody\r\n"), []string{"other@example.com"}); err != nil {
		t.Fatal(err)
	}
	queue := h.list("/api/queue")
	if len(queue) != 2 || queue[0]["release_at"] == nil || queue[1]["release_at"] != nil {
		t.Fatalf("queued: %v", queue)
	}
	heldID := fmt.Sprint(int64(queue[0]["message_id"].(float64)))
	nowID := fmt.Sprint(int64(queue[1]["message_id"].(float64)))
	if code, _ := h.do("DELETE", "/api/queue/"+nowID, nil); code != http.StatusConflict {
		t.Errorf("cancelling mail being sent: %d", code)
	}
	if code, _ := h.do("DELETE", "/api/queue/"+heldID, nil); code != http.StatusNoContent {
		t.Errorf("cancelling held mail: %d", code)
	}
	if queue := h.list("/api/queue"); len(queue) != 1 {
		t.Errorf("queued after: %v", queue)
	}
	if code, _ := h.do("DELETE", "/api/queue/later", nil); code != http.StatusBadRequest {
		t.Errorf("not a number: %d", code)
	}
	if code, _ := h.do("DELETE", "/api/queue/"+heldID, nil); code != http.StatusNotFound {
		t.Errorf("cancelled twice: %d", code)
	}
}

// lookup is DNS as a map, for the checker.
type lookup map[string][]string

func (l lookup) TXT(_ context.Context, name string) ([]string, error) { return l["TXT "+name], nil }
func (l lookup) MX(_ context.Context, name string) ([]string, error)  { return l["MX "+name], nil }

// checker is the DNS serve runs, counting the nudges a change gives it.
type checker struct {
	*dnscheck.Checker
	changed *int
}

func (c checker) Changed() { *c.changed++ }

func TestADomainShowsWhatDNSServesForEachRecord(t *testing.T) {
	changed := 0
	dns := lookup{"MX example.org": {"mail.example.com."}, "TXT _dmarc.example.org": {"v=DMARC1; p=none"}}
	h := signIn(t, newHarnessWith(t, func(st *store.Store) DNS {
		return checker{dnscheck.NewChecker(st, dns, "mail.example.com"), &changed}
	}))
	_, d := h.do("POST", "/api/domains", map[string]any{"name": "example.org"})
	if changed != 1 {
		t.Errorf("a new domain nudged the checker %d times", changed)
	}
	statuses := func(d map[string]any) []any {
		var out []any
		for _, r := range d["records"].([]any) {
			r := r.(map[string]any)
			out = append(out, fmt.Sprintf("%s %s %v %v", r["type"], r["name"], r["managed"], r["status"]))
		}
		return out
	}
	// MX is checked like the rest, and never published.
	want := []any{"MX example.org false <nil>", "TXT example.org true <nil>", "TXT _dmarc.example.org true <nil>", "TXT mailio._domainkey.example.org true <nil>"}
	if got := statuses(d); !reflect.DeepEqual(got, want) || d["dns_checked_at"] != nil {
		t.Errorf("before a check: %v %v", got, d["dns_checked_at"])
	}

	id := d["id"].(string)
	code, d := h.do("POST", "/api/domains/"+id+"/check", nil)
	want = []any{"MX example.org false ok", "TXT example.org true missing", "TXT _dmarc.example.org true different", "TXT mailio._domainkey.example.org true missing"}
	if got := statuses(d); code != http.StatusOK || !reflect.DeepEqual(got, want) || d["dns_checked_at"] == nil {
		t.Errorf("checked: %d %v %v", code, got, d["dns_checked_at"])
	}
	if found := d["records"].([]any)[2].(map[string]any)["found"]; !reflect.DeepEqual(found, []any{"v=DMARC1; p=none"}) {
		t.Errorf("what DMARC was found: %v", found)
	}

	_, d = h.do("PATCH", "/api/domains/"+id, map[string]any{"dmarc": "none"})
	if changed != 2 {
		t.Errorf("a changed domain nudged the checker %d times in all", changed)
	}
	if got := statuses(d)[2]; got != "TXT _dmarc.example.org true <nil>" {
		t.Errorf("a changed record keeps the old check: %v", got)
	}
}
