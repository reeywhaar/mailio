// Package api is the admin interface over HTTP: JSON under /api, and the bundle that calls it.
//
// Every route that changes anything requires a signed-in admin, applied where the route is
// registered so a handler cannot forget to check. See docs/api.md.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mailio/internal/config"
	"mailio/internal/store"
)

// AdminLinkPath is the page an admin link opens. serve logs it and `mailio admin reset` prints
// it, and the bundle routes it.
//
// The token rides in the fragment, which a browser never sends: a proxy in front of mailio logs
// every URL it forwards, query and all.
func AdminLinkPath(token string) string {
	return "/admin/password#token=" + url.QueryEscape(token)
}

// Mail is the running mail server, as far as the admin interface needs it. Nil in tests about
// something else.
type Mail interface {
	Certificate() *store.Certificate
	TestRelay(ctx context.Context, r *store.Relay) error
	Wake()
}

// Server is the HTTP handler.
type Server struct {
	cfg  *config.Config
	log  *slog.Logger
	st   *store.Store
	mail Mail
	mux  *http.ServeMux

	// Two buckets with two jobs: the global one bounds bcrypt on unauthenticated endpoints, the
	// per-name one stops somebody working through a password list against one admin.
	signInAll  *limiter
	signInName *limiter
}

// New builds the handler. web is the built bundle, or nil for a checkout that never built it.
func New(cfg *config.Config, log *slog.Logger, st *store.Store, mail Mail, web fs.FS) *Server {
	s := &Server{
		cfg:        cfg,
		log:        log,
		st:         st,
		mail:       mail,
		mux:        http.NewServeMux(),
		signInAll:  newLimiter(30, 2*time.Second),
		signInName: newLimiter(5, 12*time.Second),
	}

	s.mux.HandleFunc("GET /healthz", s.healthz)

	s.mux.HandleFunc("POST /api/admin-link/check", s.adminLink)
	s.mux.HandleFunc("POST /api/admin-link", s.useAdminLink)
	s.mux.HandleFunc("POST /api/session", s.signIn)
	s.mux.Handle("DELETE /api/session", s.requireAdmin(s.signOut))
	s.mux.Handle("GET /api/me", s.requireAdmin(s.me))

	s.mux.Handle("GET /api/domains", s.requireAdmin(s.listDomains))
	s.mux.Handle("POST /api/domains", s.requireAdmin(s.createDomain))
	s.mux.Handle("PATCH /api/domains/{id}", s.requireAdmin(s.updateDomain))
	s.mux.Handle("DELETE /api/domains/{id}", s.requireAdmin(s.deleteDomain))

	s.mux.Handle("GET /api/accounts", s.requireAdmin(s.listAccounts))
	s.mux.Handle("POST /api/accounts", s.requireAdmin(s.createAccount))
	s.mux.Handle("PATCH /api/accounts/{id}", s.requireAdmin(s.updateAccount))
	s.mux.Handle("POST /api/accounts/{id}/credentials", s.requireAdmin(s.addCredential))
	s.mux.Handle("POST /api/accounts/{id}/credentials/{cid}/reset", s.requireAdmin(s.resetCredential))
	s.mux.Handle("DELETE /api/accounts/{id}/credentials/{cid}", s.requireAdmin(s.deleteCredential))
	s.mux.Handle("DELETE /api/accounts/{id}", s.requireAdmin(s.deleteAccount))

	s.mux.Handle("GET /api/mail", s.requireAdmin(s.mailStatus))
	s.mux.Handle("GET /api/settings", s.requireAdmin(s.getSettings))
	s.mux.Handle("PUT /api/settings", s.requireAdmin(s.putSettings))
	s.mux.Handle("GET /api/relays", s.requireAdmin(s.listRelays))
	s.mux.Handle("POST /api/relays", s.requireAdmin(s.createRelay))
	s.mux.Handle("PUT /api/relays/{id}", s.requireAdmin(s.updateRelay))
	s.mux.Handle("DELETE /api/relays/{id}", s.requireAdmin(s.deleteRelay))
	s.mux.Handle("POST /api/relays/{id}/test", s.requireAdmin(s.testRelay))
	s.mux.Handle("GET /api/queue", s.requireAdmin(s.listQueue))
	s.mux.Handle("POST /api/queue/retry", s.requireAdmin(s.retryQueue))

	s.mux.Handle("GET /api/admins", s.requireAdmin(s.listAdmins))
	s.mux.Handle("POST /api/admins", s.requireAdmin(s.createAdmin))
	s.mux.Handle("PATCH /api/admins/{id}", s.requireAdmin(s.updateAdmin))
	s.mux.Handle("DELETE /api/admins/{id}", s.requireAdmin(s.deleteAdmin))

	// A JSON 404 for an API path nobody registered, rather than the bundle's HTML.
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		refuse(w, http.StatusNotFound, CodeNotFound, "There is no such route.")
	})
	s.mux.Handle("/", bundle(web))
	return s
}

// ServeHTTP logs every request with its status and how long it took.
//
// The path and never the query, so a secret somebody puts in a URL stays out of the log.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	s.mux.ServeHTTP(rec, r)
	if r.URL.Path == "/healthz" {
		return
	}
	s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "took", time.Since(start))
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Ping(r.Context()); err != nil {
		s.log.Error("health check failed: the database does not answer", "error", err)
		refuse(w, http.StatusServiceUnavailable, CodeInternal, "The database does not answer.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxBody is far more than any request here needs, and far less than would hurt.
const maxBody = 64 << 10

// decode reads a JSON body, refusing anything else.
//
// The content type is required because a cross-site form cannot send application/json without a
// preflight, which is the CSRF defense alongside the SameSite cookie.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		refuse(w, http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "Send JSON, with Content-Type: application/json.")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			refuse(w, http.StatusRequestEntityTooLarge, CodeBodyTooLarge, "That request is too large.")
			return false
		}
		refuse(w, http.StatusBadRequest, CodeInvalid, "That is not the JSON this route takes: "+err.Error())
		return false
	}
	return true
}

// fail maps the store's vocabulary onto a status. Anything outside it is a 500, logged, with a
// message that says nothing about why.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid):
		refuse(w, http.StatusBadRequest, CodeInvalid, err.Error())
	case errors.Is(err, store.ErrNotFound):
		refuse(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		refuse(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		refuse(w, http.StatusInternalServerError, CodeInternal, "Something went wrong on the server.")
	}
}
