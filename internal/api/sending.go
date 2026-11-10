package api

import (
	"net/http"

	"mailio/internal/ids"
	"mailio/internal/store"
)

// mailStatus is what the admin interface says about the server itself.
func (s *Server) mailStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"host": s.cfg.MailHost, "dns_api": s.cfg.UsesDNSAPI(), "staging": s.cfg.Staging, "certificate": nil}
	if s.mail != nil {
		if c := s.mail.Certificate(); c != nil {
			out["certificate"] = map[string]any{"issuer": c.Issuer, "not_after": c.NotAfter.Unix()}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// relayJSON never carries the password: the form shows whether there is one, and an empty field
// keeps it.
func relayJSON(r *store.Relay) map[string]any {
	return map[string]any{
		"id": r.ID, "label": r.Label, "host": r.Host, "port": r.Port, "security": r.Security,
		"username": r.Username, "has_password": r.HasPassword,
	}
}

// relayRequest is a relay as a form sends it.
type relayRequest struct {
	Label    string `json:"label"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (q relayRequest) relay() store.Relay {
	return store.Relay{Label: q.Label, Host: q.Host, Port: q.Port, Security: q.Security,
		Username: q.Username, Password: q.Password}
}

func (s *Server) listRelays(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.Relays(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i := range list {
		out[i] = relayJSON(&list[i])
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createRelay(w http.ResponseWriter, r *http.Request) {
	var req relayRequest
	if !decode(w, r, &req) {
		return
	}
	relay, err := s.st.CreateRelay(r.Context(), req.relay())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("relay added", "relay", relay.ID, "by", adminOf(r).ID)
	writeJSON(w, http.StatusCreated, relayJSON(relay))
}

func (s *Server) updateRelay(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Relay)
	if !ok {
		return
	}
	var req relayRequest
	if !decode(w, r, &req) {
		return
	}
	relay, err := s.st.UpdateRelay(r.Context(), id, req.relay())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("relay changed", "relay", id, "by", adminOf(r).ID)
	writeJSON(w, http.StatusOK, relayJSON(relay))
}

func (s *Server) deleteRelay(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Relay)
	if !ok {
		return
	}
	if err := s.st.DeleteRelay(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("relay deleted", "relay", id, "by", adminOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}

// testRelay signs in to a relay and sends nothing. A failure is the relay's own answer, so the
// admin can see which detail is wrong.
func (s *Server) testRelay(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Relay)
	if !ok {
		return
	}
	relay, err := s.st.RelayByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
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

func settingsJSON(set *store.Settings) map[string]any {
	orNull := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	return map[string]any{
		"unknown": set.Unknown, "dmarc": set.DMARC, "spf": set.SPF, "relay_id": orNull(set.RelayID),
		"max_message_bytes": set.MaxMessageBytes,
	}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.st.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsJSON(set))
}

// putSettings writes the defaults every domain without its own follows. A null relay is built
// in. A largest message left out stays as it is.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Unknown         store.Unknown `json:"unknown"`
		DMARC           store.DMARC   `json:"dmarc"`
		SPF             store.SPF     `json:"spf"`
		RelayID         *string       `json:"relay_id"`
		MaxMessageBytes *int64        `json:"max_message_bytes"`
	}
	if !decode(w, r, &req) {
		return
	}
	set := store.Settings{Unknown: req.Unknown, DMARC: req.DMARC, SPF: req.SPF}
	if req.RelayID != nil {
		set.RelayID = *req.RelayID
	}
	if req.MaxMessageBytes != nil {
		set.MaxMessageBytes = *req.MaxMessageBytes
	} else {
		cur, err := s.st.Settings(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		set.MaxMessageBytes = cur.MaxMessageBytes
	}
	if err := s.st.SetSettings(r.Context(), set); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("settings changed", "unknown", set.Unknown, "dmarc", set.DMARC, "spf", set.SPF,
		"relay", set.RelayID, "max_message_bytes", set.MaxMessageBytes, "by", adminOf(r).ID)
	s.changed()
	s.getSettings(w, r)
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
