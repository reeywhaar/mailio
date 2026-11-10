package api

import (
	"net/http"

	"mailio/internal/dnssync"
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
		"credentials":        list,
		"created_at":         u.CreatedAt.Unix(),
	}
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

// domainJSON shows a domain's own DMARC, SPF and relay as null when it follows the default, and
// its records as they are published, defaults applied. Its relay is "builtin" or a relay's id.
func (s *Server) domainJSON(d *store.Domain, set *store.Settings) map[string]any {
	orNull := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	relay := ""
	switch d.Via {
	case store.ViaBuiltIn:
		relay = store.ViaBuiltIn
	case store.ViaRelay:
		relay = d.RelayID
	}
	return map[string]any{
		"id":                 d.ID,
		"name":               d.Name,
		"unknown":            ownOr(d.UnknownOwn, string(d.Unknown)),
		"unknown_account_id": ownOr(d.UnknownOwn, d.UnknownAccountID),
		"dmarc":              orNull(string(d.DMARC)),
		"spf":                orNull(string(d.SPF)),
		"relay":              orNull(relay),
		"records":            dnssync.Wanted(d, set, s.cfg.MailHost),
		"created_at":         d.CreatedAt.Unix(),
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
	s.writeDomain(w, r, http.StatusCreated, d.ID)
}

// updateDomain sets a domain's own unknown addresses, DMARC, SPF and relay, "" returning one to
// the default. A field left out is left alone.
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
	}
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
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

// updateAccount sets what mail to the account does: kept, refused, or forwarded to another account.
func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Account)
	if !ok {
		return
	}
	var req struct {
		Incoming         store.Incoming `json:"incoming"`
		ForwardAccountID *string        `json:"forward_account_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	var to string
	if req.ForwardAccountID != nil {
		to = *req.ForwardAccountID
	}
	if err := s.st.SetIncoming(r.Context(), id, req.Incoming, to); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("incoming mail changed", "account", id, "incoming", req.Incoming, "by", adminOf(r).ID)
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
		Label string `json:"label"`
	}
	if !decode(w, r, &req) {
		return
	}
	password := store.GeneratePassword()
	p, err := s.st.AddCredential(r.Context(), id, req.Label, password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.answerCredential(w, r, http.StatusCreated, id, p, password)
	s.log.Info("credential added", "account", id, "credential", p.ID, "by", adminOf(r).ID)
}

// resetCredential replaces a password with a newly generated one, keeping its label and login.
func (s *Server) resetCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Account)
	if !ok {
		return
	}
	pid, ok := pathParam(w, r, "cid", ids.Credential)
	if !ok {
		return
	}
	password := store.GeneratePassword()
	p, err := s.st.ResetCredential(r.Context(), id, pid, password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.answerCredential(w, r, http.StatusOK, id, p, password)
	s.log.Info("credential reset", "account", id, "credential", pid, "by", adminOf(r).ID)
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
