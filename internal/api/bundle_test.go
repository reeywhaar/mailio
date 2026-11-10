package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// Every page is the app, titled with the mail host, so a tab says which server it is from the
// sign-in on; the assets are served as they are.
func TestThePageIsTitledWithTheMailHost(t *testing.T) {
	web := fstest.MapFS{
		"index.html":    {Data: []byte(`<!doctype html><head><title>mailio</title></head><div id="root"></div>`)},
		"assets/app.js": {Data: []byte(`console.log("<title>mailio</title>")`)},
	}
	get := func(h http.Handler, path string) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		body, _ := io.ReadAll(rec.Result().Body)
		return string(body)
	}
	h := bundle(web, "mail.example.com")
	for _, path := range []string{"/", "/login", "/index.html"} {
		if got := get(h, path); !strings.Contains(got, "<title>mail.example.com · mailio</title>") {
			t.Errorf("%s: %s", path, got)
		}
	}
	if got := get(h, "/assets/app.js"); !strings.Contains(got, "<title>mailio</title>") {
		t.Errorf("an asset was rewritten: %s", got)
	}
	// A checkout that never built the bundle says the same.
	if got := get(bundle(nil, "mail.example.com"), "/"); !strings.Contains(got, "<title>mail.example.com · mailio</title>") {
		t.Errorf("the placeholder: %s", got)
	}
}
