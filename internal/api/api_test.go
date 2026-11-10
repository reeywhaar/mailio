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
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"mailio/internal/config"
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
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, _ := seal.New(make([]byte, seal.KeySize))
	st.SetSealer(sealer)
	u, _ := url.Parse("http://mail.example.com")
	cfg := &config.Config{PublicURL: u}
	srv := httptest.NewServer(New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, nil, nil))
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
	h := newHarness(t)
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
	if code, body := h.do("POST", "/api/domains", map[string]any{"name": "reey.ru"}); code != http.StatusCreated {
		t.Fatalf("domain: %d %v", code, body)
	}
	if code, body := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@reey.ru"}); code != http.StatusCreated {
		t.Fatalf("account: %d %v", code, body)
	}

	b := h.fresh()
	if code, _ := b.do("POST", "/api/session", map[string]any{"name": "inbox@reey.ru", "password": "correct horse"}); code != http.StatusUnauthorized {
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

	code, d := h.do("POST", "/api/domains", map[string]any{"name": "Vyrtsev.com"})
	if code != http.StatusCreated || d["name"] != "vyrtsev.com" || d["unknown"] != nil {
		t.Fatalf("create domain: %d %v", code, d)
	}
	code, u := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@vyrtsev.com"})
	if code != http.StatusCreated || u["address"] != "inbox@vyrtsev.com" {
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

func TestADomainShowsTheRecordsItNeeds(t *testing.T) {
	h := signedIn(t)
	_, d := h.do("POST", "/api/domains", map[string]any{"name": "reey.ru"})
	records, _ := d["records"].([]any)
	var types []string
	for _, r := range records {
		rec := r.(map[string]any)
		types = append(types, rec["type"].(string)+" "+rec["name"].(string))
	}
	want := "TXT reey.ru,TXT _dmarc.reey.ru,TXT mailio._domainkey.reey.ru"
	if strings.Join(types, ",") != want {
		t.Errorf("records: %v", types)
	}
}

func TestAnAccountHasCredentialsEachWithItsLogin(t *testing.T) {
	h := signedIn(t)
	h.do("POST", "/api/domains", map[string]any{"name": "reey.ru"})
	_, u := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@reey.ru"})
	id := u["id"].(string)
	// The default is generated, and this answer is the one time it is shown.
	generated, _ := u["password"].(string)
	if len(generated) != store.GeneratedLength {
		t.Fatalf("generated %q", generated)
	}
	if _, err := h.st.Authenticate(context.Background(), "inbox@reey.ru", generated); err != nil {
		t.Errorf("the generated default does not sign in: %v", err)
	}
	if len(h.list("/api/accounts")[0]) == 0 || h.list("/api/accounts")[0]["password"] != nil {
		t.Error("the list carries a password")
	}
	list := u["credentials"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["label"] != "default" || list[0].(map[string]any)["login"] != "inbox@reey.ru" {
		t.Fatalf("a new account's credentials: %v", list)
	}

	code, p := h.do("POST", "/api/accounts/"+id+"/credentials", map[string]any{"label": "iPhone"})
	if code != http.StatusCreated || p["label"] != "iPhone" {
		t.Fatalf("add: %d %v", code, p)
	}
	phone := p["password"].(string)
	login := p["login"].(string)
	if !strings.HasPrefix(login, "inbox+") || !strings.HasSuffix(login, "@reey.ru") {
		t.Errorf("login %q", login)
	}
	if _, err := h.st.Authenticate(context.Background(), login, phone); err != nil {
		t.Errorf("the new login does not sign in: %v", err)
	}
	pid := p["id"].(string)

	// A new password for the same login: the old one stops.
	code, p = h.do("POST", "/api/accounts/"+id+"/credentials/"+pid+"/reset", nil)
	if code != http.StatusOK || p["login"] != login || p["password"] == phone {
		t.Fatalf("reset: %d %v", code, p)
	}
	if _, err := h.st.Authenticate(context.Background(), login, phone); err == nil {
		t.Error("the old password still signs in")
	}
	phone = p["password"].(string)
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
	h.do("POST", "/api/domains", map[string]any{"name": "reey.ru"})
	_, inbox := h.do("POST", "/api/accounts", map[string]any{"address": "inbox@reey.ru"})
	_, billing := h.do("POST", "/api/accounts", map[string]any{"address": "billing@reey.ru"})
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
	_, d := h.do("POST", "/api/domains", map[string]any{"name": "reey.ru"})
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
