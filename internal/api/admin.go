package api

import (
	"net/http"

	"mailio/internal/dnssync"
	"mailio/internal/ids"
	"mailio/internal/store"
)

func userJSON(u *store.User, passwords []store.Password) map[string]any {
	var forwardTo any
	if u.ForwardUserID != "" {
		forwardTo = u.ForwardUserID
	}
	list := make([]map[string]any, len(passwords))
	for i := range passwords {
		list[i] = passwordJSON(u, &passwords[i])
	}
	return map[string]any{
		"id":              u.ID,
		"address":         u.Address(),
		"domain_id":       u.DomainID,
		"incoming":        u.Incoming,
		"forward_user_id": forwardTo,
		"passwords":       list,
		"created_at":      u.CreatedAt.Unix(),
	}
}

// passwordJSON never carries the password: only its hash is kept.
func passwordJSON(u *store.User, p *store.Password) map[string]any {
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

func (s *Server) domainJSON(d *store.Domain) map[string]any {
	var junkTo any
	if d.UnknownUserID != "" {
		junkTo = d.UnknownUserID
	}
	return map[string]any{
		"id":              d.ID,
		"name":            d.Name,
		"unknown":         d.Unknown,
		"unknown_user_id": junkTo,
		"records":         dnssync.Wanted(d, s.cfg.MailHost),
		"created_at":      d.CreatedAt.Unix(),
	}
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
	out := make([]map[string]any, len(list))
	for i := range list {
		out[i] = s.domainJSON(&list[i])
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
	writeJSON(w, http.StatusCreated, s.domainJSON(d))
}

// updateDomain sets what the domain does with mail for addresses nobody has.
func (s *Server) updateDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.Domain)
	if !ok {
		return
	}
	var req struct {
		Unknown       store.Unknown `json:"unknown"`
		UnknownUserID *string       `json:"unknown_user_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	var userID string
	if req.UnknownUserID != nil {
		userID = *req.UnknownUserID
	}
	if err := s.st.SetUnknown(r.Context(), id, req.Unknown, userID); err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.st.DomainByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("domain changed", "domain", d.ID, "unknown", d.Unknown, "by", adminOf(r).ID)
	writeJSON(w, http.StatusOK, s.domainJSON(d))
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

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.Users(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	passwords, err := s.st.Passwords(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i := range list {
		out[i] = userJSON(&list[i], passwords[list[i].ID])
	}
	writeJSON(w, http.StatusOK, out)
}

// writeUser answers with one user as the list has it.
func (s *Server) writeUser(w http.ResponseWriter, r *http.Request, status int, id string) {
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	passwords, err := s.st.Passwords(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, userJSON(u, passwords[id]))
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address  string `json:"address"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, err := s.st.CreateUser(r.Context(), req.Address, req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("user created", "user", u.ID, "by", adminOf(r).ID)
	s.writeUser(w, r, http.StatusCreated, u.ID)
}

// updateUser sets what mail to the user does: kept, refused, or forwarded to another user.
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.User)
	if !ok {
		return
	}
	var req struct {
		Incoming      store.Incoming `json:"incoming"`
		ForwardUserID *string        `json:"forward_user_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	var to string
	if req.ForwardUserID != nil {
		to = *req.ForwardUserID
	}
	if err := s.st.SetIncoming(r.Context(), id, req.Incoming, to); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("incoming mail changed", "user", id, "incoming", req.Incoming, "by", adminOf(r).ID)
	s.writeUser(w, r, http.StatusOK, id)
}

func (s *Server) addPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.User)
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
	p, err := s.st.AddPassword(r.Context(), id, req.Label, req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("password added", "user", id, "password", p.ID, "by", adminOf(r).ID)
	writeJSON(w, http.StatusCreated, passwordJSON(u, p))
}

func (s *Server) deletePassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.User)
	if !ok {
		return
	}
	pid, ok := pathParam(w, r, "pid", ids.Password)
	if !ok {
		return
	}
	if err := s.st.DeletePassword(r.Context(), id, pid); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("password deleted", "user", id, "password", pid, "by", adminOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.User)
	if !ok {
		return
	}
	if err := s.st.DeleteUser(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("user deleted", "user", id, "by", adminOf(r).ID)
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
