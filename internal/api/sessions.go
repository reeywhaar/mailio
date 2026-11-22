package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"mailio/internal/ids"
	"mailio/internal/store"
)

// sessionJSON is one sign-in, as the admin who made it sees it. Nothing here decides anything:
// the question it answers is "do I recognize this", which a person judges better than a check.
type sessionJSON struct {
	ID      string `json:"id"`
	Current bool   `json:"current"`
	// CreatedAt and LastSeenAt together tell a session alive for a week from one an hour old.
	CreatedAt  int64 `json:"created_at"`
	LastSeenAt int64 `json:"last_seen_at"`
	ExpiresAt  int64 `json:"expires_at"`
	// Device is a guess and UserAgent what it was guessed from.
	Device    string `json:"device"`
	UserAgent string `json:"user_agent"`
}

// listSessions is the signed-in admin's sign-ins, the one used last first.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Sessions(r.Context(), adminOf(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	current := currentSessionID(r)
	out := make([]sessionJSON, 0, len(rows))
	for _, sess := range rows {
		out = append(out, sessionJSON{
			ID:         sess.ID,
			Current:    sess.ID == current,
			CreatedAt:  sess.CreatedAt.Unix(),
			LastSeenAt: sess.LastSeenAt.Unix(),
			ExpiresAt:  sess.ExpiresAt.Unix(),
			Device:     describeAgent(sess.UserAgent),
			UserAgent:  sess.UserAgent,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// revokeSession signs out one of the admin's sessions, this one included.
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathParam(w, r, "id", ids.Session)
	if !ok {
		return
	}
	a := adminOf(r)
	if err := s.st.RevokeSession(r.Context(), a.ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	if id == currentSessionID(r) {
		// The cookie names a row that is gone: the page after is signed out already.
		s.setCookie(w, "", time.Unix(0, 0))
	}
	s.log.Info("session signed out", "admin", a.ID, "session", id)
	w.WriteHeader(http.StatusNoContent)
}

// revokeOtherSessions signs out every session of the admin but this one.
func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	n, err := s.st.RevokeOtherSessions(r.Context(), a.ID, sessionOf(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("other sessions signed out", "admin", a.ID, "sessions", n)
	w.WriteHeader(http.StatusNoContent)
}

// currentSessionID names the session this request came with.
func currentSessionID(r *http.Request) string {
	if token := sessionOf(r); token != "" {
		return store.SessionID(token)
	}
	return ""
}

// describeAgent sums up a user agent as a browser and a platform. Every browser claims to be
// several others, so the most specific claim is tried first.
func describeAgent(ua string) string {
	if ua == "" {
		return ""
	}
	browser := ""
	for _, c := range []struct{ match, name string }{
		{"Edg/", "Edge"},
		{"OPR/", "Opera"},
		{"Firefox/", "Firefox"},
		{"Chrome/", "Chrome"},
		{"Safari/", "Safari"},
		{"curl/", "curl"},
	} {
		if strings.Contains(ua, c.match) {
			browser = c.name
			break
		}
	}
	platform := ""
	for _, c := range []struct{ match, name string }{
		{"iPhone", "iPhone"},
		{"iPad", "iPad"},
		{"Android", "Android"},
		{"Mac OS X", "a Mac"},
		{"Windows", "Windows"},
		{"Linux", "Linux"},
	} {
		if strings.Contains(ua, c.match) {
			platform = c.name
			break
		}
	}
	switch {
	case browser != "" && platform != "":
		return browser + " on " + platform
	case browser != "":
		return browser
	case platform != "":
		return platform
	}
	return firstToken.FindString(ua)
}

var firstToken = regexp.MustCompile(`^[^ /]+`)
