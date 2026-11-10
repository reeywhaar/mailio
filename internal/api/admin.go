package api

import (
	"net/http"

	"mailio/internal/dnscheck"
	"mailio/internal/ids"
	"mailio/internal/store"
)

func accountJSON(u *store.Account, passwords []store.Credential) map[string]any {
	var forwardTo any
	if u.ForwardAccountID != "" {
		forwardTo = u.ForwardAccountID
	}
	list := make([]map[string]any, len(passwords))
	for i := range passwords {
		list[i] = credentialJSON(u, &passwords[i])
	}
	return map[string]any{
		"id":                 u.ID,
		"address":            u.Address(),
		"domain_id":          u.DomainID,
		"incoming":           u.Incoming,
		"forward_account_id": forwardTo,
		"max_message_bytes":  ownLimit(u.MaxMessageBytes),
		"relay":              relayChoice(u.Via, u.RelayID),
		"credentials":        list,
		"created_at":         u.CreatedAt.Unix(),
	}
}

// relayChoice is how a domain's or an account's mail goes out, as the API writes it: "builtin", a
// relay's id, or null where it follows the one above.
func relayChoice(via, relayID string) any {
	switch via {
	case store.ViaBuiltIn:
		return store.ViaBuiltIn
	case store.ViaRelay:
		return relayID
	}
	return nil
}

// ownLimit is a largest message of a domain's or an account's own, or null where it follows the
// one above.
func ownLimit(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// credentialJSON carries no password: only its hash is kept. The one answer that makes it adds it.
func credentialJSON(u *store.Account, p *store.Credential) map[string]any {
	var used any
	if !p.LastUsedAt.IsZero() {
		used = p.LastUsedAt.Unix()
	}
	return map[string]any{
		"id":           p.ID,
		"label":        p.Label,
		"login":        p.LoginAddress(u),
		"created_at":   p.CreatedAt.Unix(),
		"last_used_at": used,
	}
}

func adminJSON(a *store.Admin) map[string]any {
	return map[string]any{
		"id":         a.ID,
		"name":       a.Name,
		"created_at": a.CreatedAt.Unix(),
	}
}

// domainJSON shows a domain's own DMARC, SPF, relay and largest message as null when it follows
// the default, and
// its records as they are published, defaults applied, each with what DNS served when last
// checked. Its relay is "builtin" or a relay's id.
func (s *Server) domainJSON(d *store.Domain, set *store.Settings) map[string]any {
	orNull := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	records, checkedAt := s.recordsJSON(d, set)
	return map[string]any{
		"id":                 d.ID,
		"name":               d.Name,
		"unknown":            ownOr(d.UnknownOwn, string(d.Unknown)),
		"unknown_account_id": ownOr(d.UnknownOwn, d.UnknownAccountID),
		"dmarc":              orNull(string(d.DMARC)),
		"spf":                orNull(string(d.SPF)),
		"relay":              relayChoice(d.Via, d.RelayID),
		"max_message_bytes":  ownLimit(d.MaxMessageBytes),
		"records":            records,
		"dns_checked_at":     checkedAt,
		"created_at":         d.CreatedAt.Unix(),
	}
}

// recordsJSON is each record a domain should have and its last check: a null status for none yet.
// MX is checked and never published, being set outside mailio.
func (s *Server) recordsJSON(d *store.Domain, set *store.Settings) ([]map[string]any, any) {
	var checkedAt any
	wanted := dnscheck.Wanted(d, set, s.cfg.MailHost)
	out := make([]map[string]any, len(wanted))
	for i, want := range wanted {
		var status, failure any
		found := []string{}
		if s.dns != nil {
			if r, at, ok := s.dns.Last(d.ID, want); ok {
				status, checkedAt = r.Status, at.Unix()
				if r.Found != nil {
					found = r.Found
				}
				if r.Error != "" {
					failure = r.Error
				}
			}
		}
		out[i] = map[string]any{
			"type": want.Type, "name": want.Name, "data": want.Data,
			"managed": want.Type != "MX", "status": status, "found": found, "error": failure,
		}
	}
	return out, checkedAt
}

// checkDomain looks up the domain's records now, rather than at the next round.
func (s *Server) checkDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Domain)
	if !ok {
		return
	}
	if s.dns != nil {
		if err := s.dns.CheckDomain(r.Context(), id); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.writeDomain(w, r, http.StatusOK, id)
}

// changed has the domains' records published and checked again soon.
func (s *Server) changed() {
	if s.dns != nil {
		s.dns.Changed()
	}
}

// ownOr is a domain's own value, or null where it follows the default.
func ownOr(own bool, v string) any {
	if !own || v == "" {
		return nil
	}
	return v
}

// writeDomain answers with one domain as the list has it.
func (s *Server) writeDomain(w http.ResponseWriter, r *http.Request, status int, id string) {
	d, err := s.st.DomainByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	set, err := s.st.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, s.domainJSON(d, set))
}

// pathID reads {id} and refuses one that cannot be an id, so a typo is a 400 rather than a 404
// that looks like a missing row.
func pathID(w http.ResponseWriter, r *http.Request, prefix string) (string, bool) {
	return pathParam(w, r, "id", prefix)
}

func pathParam(w http.ResponseWriter, r *http.Request, key, prefix string) (string, bool) {
	id := r.PathValue(key)
	if !ids.Valid(prefix, id) {
		refuse(w, http.StatusBadRequest, CodeInvalid, "That is not an id.")
		return "", false
	}
	return id, true
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.Domains(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	set, err := s.st.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i := range list {
		out[i] = s.domainJSON(&list[i], set)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	d, err := s.st.CreateDomain(r.Context(), req.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("domain added", "domain", d.ID, "by", adminOf(r).ID)
	s.changed()
	s.writeDomain(w, r, http.StatusCreated, d.ID)
}

// updateDomain sets a domain's own unknown addresses, DMARC, SPF, relay and largest message, ""
// or 0 returning one to the default. A field left out is left alone.
func (s *Server) updateDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Domain)
	if !ok {
		return
	}
	var req struct {
		Unknown          *store.Unknown `json:"unknown"`
		UnknownAccountID *string        `json:"unknown_account_id"`
		DMARC            *store.DMARC   `json:"dmarc"`
		SPF              *store.SPF     `json:"spf"`
		Relay            *string        `json:"relay"`
		MaxMessageBytes  *int64         `json:"max_message_bytes"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	// First: the one most likely refused, for a number out of range, which then changes nothing.
	if req.MaxMessageBytes != nil {
		if err := s.st.SetDomainMessageLimit(ctx, id, *req.MaxMessageBytes); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("domain changed", "domain", id, "max_message_bytes", *req.MaxMessageBytes, "by", adminOf(r).ID)
	}
	if req.Unknown != nil {
		var accountID string
		if req.UnknownAccountID != nil {
			accountID = *req.UnknownAccountID
		}
		if err := s.st.SetUnknown(ctx, id, *req.Unknown, accountID); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("domain changed", "domain", id, "unknown", *req.Unknown, "by", adminOf(r).ID)
	}
	if req.DMARC != nil {
		if err := s.st.SetDMARC(ctx, id, *req.DMARC); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("domain changed", "domain", id, "dmarc", *req.DMARC, "by", adminOf(r).ID)
	}
	if req.SPF != nil {
		if err := s.st.SetSPF(ctx, id, *req.SPF); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("domain changed", "domain", id, "spf", *req.SPF, "by", adminOf(r).ID)
	}
	if req.Relay != nil {
		if err := s.st.SetDomainVia(ctx, id, *req.Relay); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("domain changed", "domain", id, "relay", *req.Relay, "by", adminOf(r).ID)
	}
	s.changed()
	s.writeDomain(w, r, http.StatusOK, id)
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Domain)
	if !ok {
		return
	}
	if err := s.st.DeleteDomain(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("domain deleted", "domain", id, "by", adminOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.Accounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	passwords, err := s.st.Credentials(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i := range list {
		out[i] = accountJSON(&list[i], passwords[list[i].ID])
	}
	writeJSON(w, http.StatusOK, out)
}

// writeAccount answers with one account as the list has it.
func (s *Server) writeAccount(w http.ResponseWriter, r *http.Request, status int, id string) {
	u, err := s.st.AccountByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	passwords, err := s.st.Credentials(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, accountJSON(u, passwords[id]))
}

// createAccount makes an account from an address alone. Its default password is generated, and this
// answer is the one time it is shown: only its hash is kept.
func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}
	if !decode(w, r, &req) {
		return
	}
	password := store.GeneratePassword()
	a, err := s.st.CreateAccount(r.Context(), req.Address, password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("account created", "account", a.ID, "by", adminOf(r).ID)
	creds, err := s.st.Credentials(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := accountJSON(a, creds[a.ID])
	out["password"] = password
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, out)
}

// updateAccount sets what mail to the account does, kept, refused, or forwarded to another
// account, its own largest message, 0 returning it to its domain's, and its own relay, ""
// returning it to its domain's. A field left out is left alone.
func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Account)
	if !ok {
		return
	}
	var req struct {
		Incoming         *store.Incoming `json:"incoming"`
		ForwardAccountID *string         `json:"forward_account_id"`
		MaxMessageBytes  *int64          `json:"max_message_bytes"`
		Relay            *string         `json:"relay"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.MaxMessageBytes != nil {
		if err := s.st.SetAccountMessageLimit(r.Context(), id, *req.MaxMessageBytes); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("account changed", "account", id, "max_message_bytes", *req.MaxMessageBytes, "by", adminOf(r).ID)
	}
	if req.Relay != nil {
		if err := s.st.SetAccountVia(r.Context(), id, *req.Relay); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("account changed", "account", id, "relay", *req.Relay, "by", adminOf(r).ID)
	}
	if req.Incoming != nil {
		var to string
		if req.ForwardAccountID != nil {
			to = *req.ForwardAccountID
		}
		if err := s.st.SetIncoming(r.Context(), id, *req.Incoming, to); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("incoming mail changed", "account", id, "incoming", *req.Incoming, "by", adminOf(r).ID)
	}
	s.writeAccount(w, r, http.StatusOK, id)
}

// addCredential gives an account another password, generated, with a login of its own. This answer is
// the one time the password is shown.
func (s *Server) addCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Account)
	if !ok {
		return
	}
	var req struct {
		Label    string `json:"label"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	password := orGenerated(req.Password)
	p, err := s.st.AddCredential(r.Context(), id, req.Label, password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.answerCredential(w, r, http.StatusCreated, id, p, password)
	s.log.Info("credential added", "account", id, "credential", p.ID, "by", adminOf(r).ID)
}

// resetCredential replaces a password, keeping its label and login.
func (s *Server) resetCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Account)
	if !ok {
		return
	}
	pid, ok := pathParam(w, r, "cid", ids.Credential)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	password := orGenerated(req.Password)
	p, err := s.st.ResetCredential(r.Context(), id, pid, password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.answerCredential(w, r, http.StatusOK, id, p, password)
	s.log.Info("credential reset", "account", id, "credential", pid, "by", adminOf(r).ID)
}

// orGenerated is the password an admin typed, or, when they typed none, a generated one: some
// mail clients are particular about what a password may hold, and the admin knows which.
func orGenerated(password string) string {
	if password == "" {
		return store.GeneratePassword()
	}
	return password
}

// answerCredential is a credential with, this once, its password.
func (s *Server) answerCredential(w http.ResponseWriter, r *http.Request, status int, accountID string, c *store.Credential, password string) {
	a, err := s.st.AccountByID(r.Context(), accountID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := credentialJSON(a, c)
	out["password"] = password
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, out)
}

func (s *Server) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Account)
	if !ok {
		return
	}
	pid, ok := pathParam(w, r, "cid", ids.Credential)
	if !ok {
		return
	}
	if err := s.st.DeleteCredential(r.Context(), id, pid); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("credential deleted", "account", id, "credential", pid, "by", adminOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Account)
	if !ok {
		return
	}
	if err := s.st.DeleteAccount(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("account deleted", "account", id, "by", adminOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAdmins(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.Admins(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i := range list {
		out[i] = adminJSON(&list[i])
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createAdmin(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decode(w, r, &req) {
		return
	}
	a, err := s.st.CreateAdmin(r.Context(), req.Name, req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("admin created", "admin", a.ID, "by", adminOf(r).ID)
	writeJSON(w, http.StatusCreated, adminJSON(a))
}

// updateAdmin sets a password and signs that admin out everywhere but here.
func (s *Server) updateAdmin(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Admin)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.st.SetAdminPassword(r.Context(), id, req.Password, sessionOf(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("admin password changed", "admin", id, "by", adminOf(r).ID)
	a, err := s.st.AdminByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adminJSON(a))
}

func (s *Server) deleteAdmin(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Admin)
	if !ok {
		return
	}
	if err := s.st.DeleteAdmin(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("admin deleted", "admin", id, "by", adminOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}
