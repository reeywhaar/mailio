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

type credentials struct {
	Address  string `json:"address"`
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
	u, err := s.st.UseAdminLink(r.Context(), req.Token, req.Address, req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.issue(w, r, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("admin link used", "user", u.ID)
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
	if !s.signInUser.allow(strings.ToLower(strings.TrimSpace(req.Address))) {
		refuse(w, http.StatusTooManyRequests, CodeRateLimited, "Too many sign-in attempts for that address. Wait a minute.")
		return
	}
	u, err := s.st.Authenticate(r.Context(), req.Address, req.Password)
	if errors.Is(err, store.ErrNotFound) {
		s.log.Warn(PasswordRefused, "ip", clientIP(r), "via", "admin")
		refuse(w, http.StatusUnauthorized, CodeUnauthenticated, "That address and password do not match.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !u.Admin {
		s.log.Info("sign-in refused: not an admin", "user", u.ID)
		refuse(w, http.StatusForbidden, CodeForbidden, "Only an admin can sign in here.")
		return
	}
	if err := s.issue(w, r, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("signed in", "user", u.ID)
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
	s.log.Info("signed out", "user", userOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userJSON(userOf(r)))
}

// issue starts a session and hands the browser its cookie.
func (s *Server) issue(w http.ResponseWriter, r *http.Request, userID string) error {
	token, err := store.Secret()
	if err != nil {
		return err
	}
	sess, err := s.st.CreateSession(r.Context(), token, userID, r.UserAgent())
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

// requireAdmin admits a request carrying a live session of a user who is still an admin.
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
		u, err := s.st.UserByID(r.Context(), sess.UserID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
		// Losing the flag deletes the sessions, so this is a race with that rather than a state
		// anybody stays in.
		if u == nil || !u.Admin {
			s.setCookie(w, "", time.Unix(0, 0))
			refuse(w, http.StatusUnauthorized, CodeUnauthenticated, "Sign in first.")
			return
		}
		if expires, moved, err := s.st.TouchSession(r.Context(), c.Value, r.UserAgent()); err == nil && moved {
			s.setCookie(w, c.Value, expires)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxUser, u)))
	})
}

type ctxKey int

const ctxUser ctxKey = iota

func userOf(r *http.Request) *store.User {
	u, _ := r.Context().Value(ctxUser).(*store.User)
	return u
}
