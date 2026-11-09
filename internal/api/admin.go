package api

import (
	"net/http"

	"mailio/internal/ids"
	"mailio/internal/store"
)

func userJSON(u *store.User) map[string]any {
	return map[string]any{
		"id":         u.ID,
		"address":    u.Address(),
		"domain_id":  u.DomainID,
		"admin":      u.Admin,
		"created_at": u.CreatedAt.Unix(),
	}
}

func domainJSON(d *store.Domain) map[string]any {
	var junkTo any
	if d.UnknownUserID != "" {
		junkTo = d.UnknownUserID
	}
	return map[string]any{
		"id":              d.ID,
		"name":            d.Name,
		"unknown":         d.Unknown,
		"unknown_user_id": junkTo,
		"created_at":      d.CreatedAt.Unix(),
	}
}

// pathID reads {id} and refuses one that cannot be an id, so a typo is a 400 rather than a 404
// that looks like a missing row.
func pathID(w http.ResponseWriter, r *http.Request, prefix string) (string, bool) {
	id := r.PathValue("id")
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
		out[i] = domainJSON(&list[i])
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
	s.log.Info("domain added", "domain", d.ID, "by", userOf(r).ID)
	writeJSON(w, http.StatusCreated, domainJSON(d))
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
	s.log.Info("domain changed", "domain", d.ID, "unknown", d.Unknown, "by", userOf(r).ID)
	writeJSON(w, http.StatusOK, domainJSON(d))
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
	s.log.Info("domain deleted", "domain", id, "by", userOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.Users(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, len(list))
	for i := range list {
		out[i] = userJSON(&list[i])
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		credentials
		Admin bool `json:"admin"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, err := s.st.CreateUser(r.Context(), req.Address, req.Password, req.Admin)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("user created", "user", u.ID, "admin", u.Admin, "by", userOf(r).ID)
	writeJSON(w, http.StatusCreated, userJSON(u))
}

// updateUser changes a password, the admin flag, or both. A field left out is left alone.
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, ids.User)
	if !ok {
		return
	}
	var req struct {
		Password *string `json:"password"`
		Admin    *bool   `json:"admin"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Password != nil {
		if err := s.st.SetPassword(r.Context(), id, *req.Password); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("password changed", "user", id, "by", userOf(r).ID)
	}
	if req.Admin != nil {
		if err := s.st.SetAdmin(r.Context(), id, *req.Admin); err != nil {
			s.fail(w, r, err)
			return
		}
		s.log.Info("admin changed", "user", id, "admin", *req.Admin, "by", userOf(r).ID)
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, userJSON(u))
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
	s.log.Info("user deleted", "user", id, "by", userOf(r).ID)
	w.WriteHeader(http.StatusNoContent)
}
