package api

import (
	"net/http"

	"mailio/internal/store"
)

// mailStatus is what the admin interface says about the server itself.
func (s *Server) mailStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"host": s.cfg.MailHost, "dns_api": s.cfg.DNSAPI != "", "certificate": nil}
	if s.mail != nil {
		if c := s.mail.Certificate(); c != nil {
			out["certificate"] = map[string]any{"issuer": c.Issuer, "not_after": c.NotAfter.Unix()}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// relayJSON never carries the password: the form shows whether there is one, and an empty field
// keeps it.
func relayJSON(r *store.Relay) any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"host": r.Host, "port": r.Port, "security": r.Security, "username": r.Username,
		"has_password": r.Password != "",
	}
}

func (s *Server) getRelay(w http.ResponseWriter, r *http.Request) {
	relay, err := s.st.Relay(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, relayJSON(relay))
}

func (s *Server) putRelay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Security string `json:"security"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	err := s.st.SetRelay(r.Context(), store.Relay{Host: req.Host, Port: req.Port, Security: req.Security,
		Username: req.Username, Password: req.Password})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("relay set", "by", adminOf(r).ID)
	s.getRelay(w, r)
}

func (s *Server) deleteRelay(w http.ResponseWriter, r *http.Request) {
	if err := s.st.ClearRelay(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("relay removed", "by", adminOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}

// testRelay signs in to the stored relay and sends nothing. A failure is the relay's own answer,
// so the admin can see which detail is wrong.
func (s *Server) testRelay(w http.ResponseWriter, r *http.Request) {
	relay, err := s.st.Relay(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if relay == nil {
		refuse(w, http.StatusConflict, CodeConflict, "There is no relay to test.")
		return
	}
	if s.mail == nil {
		refuse(w, http.StatusConflict, CodeConflict, "The mail server is not running.")
		return
	}
	if err := s.mail.TestRelay(r.Context(), relay); err != nil {
		refuse(w, http.StatusBadGateway, CodeRelayFailed, "The relay said: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listQueue(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.Outbound(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i, o := range list {
		out[i] = map[string]any{
			"id": o.ID, "sender": o.Sender, "recipient": o.Recipient, "attempts": o.Attempts,
			"last_error": o.LastError, "next_at": o.NextAt.Unix(), "created_at": o.CreatedAt.Unix(),
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// retryQueue makes everything due now: the relay was just fixed.
func (s *Server) retryQueue(w http.ResponseWriter, r *http.Request) {
	if err := s.st.RetryOutbound(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.mail != nil {
		s.mail.Wake()
	}
	s.log.Info("queue retried", "by", adminOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}
