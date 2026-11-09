package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"mailio/internal/store"
)

const cookieName = "mailio_session"

// PasswordRefused is the message of the one line logged for every wrong password, with the
// caller's "ip". fail2ban matches it, so it is a contract: see docs/deploy.md.
const PasswordRefused = "password refused"

// credentials are an admin's: a name, not an address.
type credentials struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// adminLink tells the page whether a link is live before somebody types a password into it. A
// POST because the token belongs in a body, out of every log a URL lands in.
func (s *Server) adminLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	expires, err := s.st.AdminLinkExpiry(r.Context(), req.Token)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"expires_at": expires.Unix()})
}

// useAdminLink spends a link and signs the admin in, because being shown a sign-in form straight
// afterwards is asking somebody to prove what they just proved.
func (s *Server) useAdminLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
		credentials
	}
	if !decode(w, r, &req) {
		return
	}
	if !s.signInAll.allow("") {
		refuse(w, http.StatusTooManyRequests, CodeRateLimited, "Too many attempts. Wait a minute.")
		return
	}
	a, err := s.st.UseAdminLink(r.Context(), req.Token, req.Name, req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.issue(w, r, a.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("admin link used", "admin", a.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decode(w, r, &req) {
		return
	}
	if !s.signInAll.allow("") {
		refuse(w, http.StatusTooManyRequests, CodeRateLimited, "Too many sign-in attempts. Wait a minute.")
		return
	}
	if !s.signInName.allow(strings.ToLower(strings.TrimSpace(req.Name))) {
		refuse(w, http.StatusTooManyRequests, CodeRateLimited, "Too many sign-in attempts for that name. Wait a minute.")
		return
	}
	a, err := s.st.AuthenticateAdmin(r.Context(), req.Name, req.Password)
	if errors.Is(err, store.ErrNotFound) {
		s.log.Warn(PasswordRefused, "ip", s.clientIP(r), "via", "admin")
		refuse(w, http.StatusUnauthorized, CodeUnauthenticated, "That name and password do not match.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.issue(w, r, a.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("signed in", "admin", a.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		if err := s.st.DeleteSession(r.Context(), c.Value); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.setCookie(w, "", time.Unix(0, 0))
	s.log.Info("signed out", "admin", adminOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, adminJSON(adminOf(r)))
}

// issue starts a session and hands the browser its cookie.
func (s *Server) issue(w http.ResponseWriter, r *http.Request, adminID string) error {
	token, err := store.Secret()
	if err != nil {
		return err
	}
	sess, err := s.st.CreateSession(r.Context(), token, adminID, r.UserAgent())
	if err != nil {
		return err
	}
	s.setCookie(w, token, sess.ExpiresAt)
	return nil
}

func (s *Server) setCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// requireAdmin admits a request carrying a live session of an admin who still exists.
func (s *Server) requireAdmin(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			refuse(w, http.StatusUnauthorized, CodeUnauthenticated, "Sign in first.")
			return
		}
		sess, err := s.st.SessionByToken(r.Context(), c.Value)
		if errors.Is(err, store.ErrNotFound) {
			s.setCookie(w, "", time.Unix(0, 0))
			refuse(w, http.StatusUnauthorized, CodeUnauthenticated, "Sign in first.")
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		a, err := s.st.AdminByID(r.Context(), sess.AdminID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
		// Deleting an admin deletes their sessions, so this is a race with that rather than a
		// state anybody stays in.
		if a == nil {
			s.setCookie(w, "", time.Unix(0, 0))
			refuse(w, http.StatusUnauthorized, CodeUnauthenticated, "Sign in first.")
			return
		}
		if expires, moved, err := s.st.TouchSession(r.Context(), c.Value, r.UserAgent()); err == nil && moved {
			s.setCookie(w, c.Value, expires)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxAdmin, a)))
	})
}

type ctxKey int

const ctxAdmin ctxKey = iota

func adminOf(r *http.Request) *store.Admin {
	a, _ := r.Context().Value(ctxAdmin).(*store.Admin)
	return a
}

// sessionOf is the cookie the request signed in with, so a change of one's own password keeps the
// session making it.
func sessionOf(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
