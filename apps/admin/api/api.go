// Package api is the JSON API that the SPA calls. Every handler requires a
// session with the admin role; the types come from ../openapi.yaml.
package api

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.1 -config oapi-codegen.yaml ../openapi.yaml

import (
	"log/slog"
	"net/http"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
)

type Service struct {
	Sessions  core.SessionReader
	AdminRole string
	Log       *slog.Logger
}

func NewService(sessions core.SessionReader, adminRole string, log *slog.Logger) *Service {
	return &Service{Sessions: sessions, AdminRole: adminRole, Log: log}
}

func (s *Service) MeHandler(w http.ResponseWriter, r *http.Request) {
	session, ok := s.admin(w, r)
	if !ok {
		return
	}
	core.WriteJSON(w, http.StatusOK, Me{
		Subject:   session.Subject,
		Name:      session.Name,
		Email:     session.Email,
		Roles:     session.Roles,
		AdminRole: s.AdminRole,
		ExpiresAt: session.Expires,
	})
}

// NotFoundHandler answers unknown /api/ paths, which would otherwise get the
// SPA's index.html.
func (s *Service) NotFoundHandler(w http.ResponseWriter, r *http.Request) {
	core.WriteError(w, http.StatusNotFound, "not found")
}

// admin returns the session of an admin, or writes 401 or 403. The SPA sends
// the browser to the login on 401.
func (s *Service) admin(w http.ResponseWriter, r *http.Request) (core.Session, bool) {
	w.Header().Set("Cache-Control", "no-store")
	session, ok := s.Sessions.Session(r)
	if !ok {
		core.WriteError(w, http.StatusUnauthorized, "not signed in")
		return core.Session{}, false
	}
	if !session.HasRole(s.AdminRole) {
		s.Log.InfoContext(r.Context(), "access denied", "sub", session.Subject, "roles", session.Roles)
		core.WriteError(w, http.StatusForbidden, "access denied")
		return core.Session{}, false
	}
	return session, true
}
