package api

import (
	"errors"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// placeholder is what a checkout that never built the bundle serves, so the API is usable
// without Node.
const placeholder = `<!doctype html><meta charset="utf-8"><title>mailio</title>
<p>The admin interface is not built. Run <code>npm --prefix web ci && npm --prefix web run build</code>.</p>`

// title is the bundle's own, which the page is served with the mail host in front of.
const title = "<title>mailio</title>"

// bundle serves the built admin interface: a file when one exists at the path, and index.html
// for every other path, which the app routes itself.
//
// index.html is never cached, so a deploy is picked up on the next load; the hashed assets
// beside it can be cached forever. Its title names the mail host, written in here rather than by
// the app, so a tab says which server it is before anybody signs in.
func bundle(web fs.FS, host string) http.Handler {
	named := "<title>" + html.EscapeString(host+" · mailio") + "</title>"
	if web == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(strings.Replace(placeholder, title, named, 1)))
		})
	}
	files := http.FileServerFS(web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(web, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "unreadable", http.StatusInternalServerError)
				return
			}
		}
		page, err := fs.ReadFile(web, "index.html")
		if err != nil {
			http.Error(w, "unreadable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(strings.Replace(string(page), title, named, 1)))
	})
}
