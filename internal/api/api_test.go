package api

import (
	"bytes"
	"context"
	"encoding/json"
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
	// An admin is not a mailbox, so the link made no domain and no user.
	if len(h.list("/api/domains")) != 0 || len(h.list("/api/users")) != 0 {
		t.Error("the admin link made a domain or a user")
	}
	if code, _ := h.do("POST", "/api/admin-link/check", map[string]any{"token": token}); code != http.StatusNotFound {
		t.Errorf("a spent link checks as %d", code)
	}
}

func TestAUserCannotSignInHereOnlyAnAdmin(t *testing.T) {
	h := signedIn(t)
	if code, body := h.do("POST", "/api/domains", map[string]any{"name": "reey.ru"}); code != http.StatusCreated {
		t.Fatalf("domain: %d %v", code, body)
	}
	if code, body := h.do("POST", "/api/users", map[string]any{"address": "inbox@reey.ru", "password": "another one"}); code != http.StatusCreated {
		t.Fatalf("user: %d %v", code, body)
	}

	b := h.fresh()
	if code, _ := b.do("POST", "/api/session", map[string]any{"name": "inbox@reey.ru", "password": "another one"}); code != http.StatusUnauthorized {
		t.Errorf("a user: %d", code)
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
		{"GET", "/api/users"}, {"POST", "/api/users"}, {"PATCH", "/api/users/u_x"}, {"DELETE", "/api/users/u_x"},
		{"POST", "/api/users/u_x/passwords"}, {"DELETE", "/api/users/u_x/passwords/pw_x"},
		{"GET", "/api/admins"}, {"POST", "/api/admins"}, {"PATCH", "/api/admins/ad_x"}, {"DELETE", "/api/admins/ad_x"},
		{"GET", "/api/me"}, {"DELETE", "/api/session"},
	} {
		if code, _ := h.do(c[0], c[1], map[string]any{}); code != http.StatusUnauthorized {
			t.Errorf("%s %s: %d", c[0], c[1], code)
		}
	}
}

func TestAnAdminManagesDomainsAndUsers(t *testing.T) {
	h := signedIn(t)

	code, d := h.do("POST", "/api/domains", map[string]any{"name": "Vyrtsev.com"})
	if code != http.StatusCreated || d["name"] != "vyrtsev.com" || d["unknown"] != "reject" {
		t.Fatalf("create domain: %d %v", code, d)
	}
	code, u := h.do("POST", "/api/users", map[string]any{"address": "inbox@vyrtsev.com", "password": "correct horse"})
	if code != http.StatusCreated || u["address"] != "inbox@vyrtsev.com" {
		t.Fatalf("create user: %d %v", code, u)
	}

	domainID, userID := d["id"].(string), u["id"].(string)
	code, d = h.do("PATCH", "/api/domains/"+domainID, map[string]any{"unknown": "junk", "unknown_user_id": userID})
	if code != http.StatusOK || d["unknown"] != "junk" || d["unknown_user_id"] != userID {
		t.Fatalf("set junk: %d %v", code, d)
	}
	if code, _ := h.do("DELETE", "/api/users/"+userID, nil); code != http.StatusConflict {
		t.Errorf("deleting the junk user: %d", code)
	}
	if code, _ := h.do("PATCH", "/api/domains/"+domainID, map[string]any{"unknown": "drop"}); code != http.StatusOK {
		t.Errorf("set drop: %d", code)
	}

	if len(h.list("/api/users")) != 1 || len(h.list("/api/domains")) != 1 {
		t.Error("the lists do not have what was made")
	}

	if code, _ := h.do("DELETE", "/api/users/"+userID, nil); code != http.StatusNoContent {
		t.Errorf("delete user: %d", code)
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
	if code, _ := h.do("DELETE", "/api/users/nope", nil); code != http.StatusBadRequest {
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

func TestTheRelayIsSetAndItsPasswordNeverComesBack(t *testing.T) {
	h := signedIn(t)
	code, r := h.do("PUT", "/api/relay", map[string]any{"host": "smtp.relay.example", "port": 587, "security": "starttls", "username": "me", "password": "relay secret"})
	if code != http.StatusOK || r["has_password"] != true || r["password"] != nil {
		t.Fatalf("set: %d %v", code, r)
	}
	// A new port keeps the password the form never showed.
	code, r = h.do("PUT", "/api/relay", map[string]any{"host": "smtp.relay.example", "port": 465, "security": "implicit", "username": "me", "password": ""})
	if code != http.StatusOK || r["has_password"] != true || r["port"] != float64(465) {
		t.Fatalf("change port: %d %v", code, r)
	}
	stored, _ := h.st.Relay(context.Background())
	if stored.Password != "relay secret" {
		t.Errorf("the password became %q", stored.Password)
	}
	if code, _ := h.do("PUT", "/api/relay", map[string]any{"host": "smtp.relay.example", "port": 0, "security": "starttls"}); code != http.StatusBadRequest {
		t.Errorf("port 0: %d", code)
	}
	if code, _ := h.do("DELETE", "/api/relay", nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if code, body := h.do("GET", "/api/relay", nil); code != http.StatusOK || body != nil {
		t.Errorf("after delete: %d %v", code, body)
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
	want := "MX reey.ru,TXT reey.ru,TXT _dmarc.reey.ru,TXT mailio._domainkey.reey.ru"
	if strings.Join(types, ",") != want {
		t.Errorf("records: %v", types)
	}
}

func TestAUserHasPasswordsEachWithItsLogin(t *testing.T) {
	h := signedIn(t)
	h.do("POST", "/api/domains", map[string]any{"name": "reey.ru"})
	_, u := h.do("POST", "/api/users", map[string]any{"address": "inbox@reey.ru", "password": "correct horse"})
	id := u["id"].(string)
	list := u["passwords"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["label"] != "default" || list[0].(map[string]any)["login"] != "inbox@reey.ru" {
		t.Fatalf("a new user's passwords: %v", list)
	}

	code, p := h.do("POST", "/api/users/"+id+"/passwords", map[string]any{"label": "iPhone", "password": "phone password"})
	if code != http.StatusCreated || p["label"] != "iPhone" || p["password"] != nil {
		t.Fatalf("add: %d %v", code, p)
	}
	login := p["login"].(string)
	if !strings.HasPrefix(login, "inbox+") || !strings.HasSuffix(login, "@reey.ru") {
		t.Errorf("login %q", login)
	}
	if _, err := h.st.Authenticate(context.Background(), login, "phone password"); err != nil {
		t.Errorf("the new login does not sign in: %v", err)
	}
	pid := p["id"].(string)
	if code, _ := h.do("DELETE", "/api/users/"+id+"/passwords/"+pid, nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if _, err := h.st.Authenticate(context.Background(), login, "phone password"); err == nil {
		t.Error("a deleted password still signs in")
	}
	if code, _ := h.do("DELETE", "/api/users/"+id+"/passwords/"+pid, nil); code != http.StatusNotFound {
		t.Errorf("deleting it twice: %d", code)
	}
}

func TestAUserRefusesOrForwardsItsMail(t *testing.T) {
	h := signedIn(t)
	h.do("POST", "/api/domains", map[string]any{"name": "reey.ru"})
	_, inbox := h.do("POST", "/api/users", map[string]any{"address": "inbox@reey.ru", "password": "correct horse"})
	_, billing := h.do("POST", "/api/users", map[string]any{"address": "billing@reey.ru", "password": "correct horse"})
	if billing["incoming"] != "keep" || billing["forward_user_id"] != nil {
		t.Errorf("a new user: %v", billing)
	}
	code, u := h.do("PATCH", "/api/users/"+billing["id"].(string), map[string]any{"incoming": "forward", "forward_user_id": inbox["id"]})
	if code != http.StatusOK || u["incoming"] != "forward" || u["forward_user_id"] != inbox["id"] {
		t.Errorf("forward: %d %v", code, u)
	}
	code, u = h.do("PATCH", "/api/users/"+billing["id"].(string), map[string]any{"incoming": "reject"})
	if code != http.StatusOK || u["incoming"] != "reject" || u["forward_user_id"] != nil {
		t.Errorf("reject: %d %v", code, u)
	}
	if code, _ := h.do("PATCH", "/api/users/"+billing["id"].(string), map[string]any{"incoming": "forward"}); code != http.StatusBadRequest {
		t.Errorf("forward to nobody: %d", code)
	}
}
