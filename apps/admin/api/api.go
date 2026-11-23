// Package api calls Zitadel with the app's service account, never the admin's token. Organizations
// are created and deleted only as tenants (ADR-0012), so no route does it in Zitadel.
package api

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.1 -config oapi-codegen.yaml ../openapi.yaml

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

type Zitadel interface {
	ServiceAccount(ctx context.Context) (zitadel.ServiceAccount, error)

	ListOrganizations(ctx context.Context, f zitadel.OrganizationFilter, p zitadel.Page) (zitadel.OrganizationList, error)
	GetOrganization(ctx context.Context, id string) (zitadel.Organization, error)
	DefaultOrganizationID(ctx context.Context) (string, error)
	UpdateOrganization(ctx context.Context, id, name string) error
	DeactivateOrganization(ctx context.Context, id string) error
	ActivateOrganization(ctx context.Context, id string) error

	ListUsers(ctx context.Context, f zitadel.UserFilter, p zitadel.Page) (zitadel.UserList, error)
	GetUser(ctx context.Context, id string) (zitadel.User, error)
	CreateHumanUser(ctx context.Context, u zitadel.NewHumanUser) (string, error)
	CreateMachineUser(ctx context.Context, u zitadel.NewMachineUser) (string, error)
	UpdateUser(ctx context.Context, id string, u zitadel.UserUpdate) error
	SetPassword(ctx context.Context, id, password string, changeRequired bool) error
	DeactivateUser(ctx context.Context, id string) error
	ReactivateUser(ctx context.Context, id string) error
	LockUser(ctx context.Context, id string) error
	UnlockUser(ctx context.Context, id string) error
	DeleteUser(ctx context.Context, id string) error
	AddSecret(ctx context.Context, id string) (string, error)
	RemoveSecret(ctx context.Context, id string) error

	ListAuthorizations(ctx context.Context, f zitadel.AuthorizationFilter, p zitadel.Page) (zitadel.AuthorizationList, error)
	GetAuthorization(ctx context.Context, id string) (zitadel.Authorization, error)
	CreateAuthorization(ctx context.Context, userID, projectID, organizationID string, roleKeys []string) (string, error)
	UpdateAuthorization(ctx context.Context, id string, roleKeys []string) error
	DeleteAuthorization(ctx context.Context, id string) error

	ListProjects(ctx context.Context, f zitadel.ProjectFilter, p zitadel.Page) (zitadel.ProjectList, error)
	GetProject(ctx context.Context, id string) (zitadel.Project, error)
	ListProjectRoles(ctx context.Context, projectID string, p zitadel.Page) ([]zitadel.ProjectRole, error)

	// The instance's branding goes through Platform.
	BrandingClient
	CreateOrgBranding(ctx context.Context, orgID string, settings zitadel.BrandingSettings) error
	ResetOrgBranding(ctx context.Context, orgID string) error
}

var _ Zitadel = (*zitadel.Client)(nil)

type Service struct {
	Sessions core.SessionReader
	Zitadel  Zitadel
	Platform PlatformZitadel
	// nil when TENANTS_URL is not set; the tenant routes then answer 503.
	Tenants   Tenants
	AdminRole string
	Log       *slog.Logger
	csrf      *http.CrossOriginProtection
}

// publicURL is the origin state-changing requests must come from.
func NewService(sessions core.SessionReader, zitadel Zitadel, platform PlatformZitadel, adminRole, publicURL string, log *slog.Logger) *Service {
	csrf := http.NewCrossOriginProtection()
	if u, err := url.Parse(publicURL); err == nil && u.Scheme != "" && u.Host != "" {
		if err := csrf.AddTrustedOrigin(u.Scheme + "://" + u.Host); err != nil {
			log.Warn("PUBLIC_URL is not a trusted origin", "url", publicURL, "err", err)
		}
	}
	return &Service{Sessions: sessions, Zitadel: zitadel, Platform: platform, AdminRole: adminRole, Log: log, csrf: csrf}
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me", s.MeHandler)
	mux.HandleFunc("GET /api/overview", s.read(s.overview))

	mux.HandleFunc("GET /api/orgs", s.read(s.listOrgs))
	mux.HandleFunc("GET /api/orgs/{orgId}", s.read(s.getOrg))
	mux.HandleFunc("PATCH /api/orgs/{orgId}", s.change(s.updateOrg))
	mux.HandleFunc("POST /api/orgs/{orgId}/deactivate", s.change(s.deactivateOrg))
	mux.HandleFunc("POST /api/orgs/{orgId}/activate", s.change(s.activateOrg))

	mux.HandleFunc("GET /api/users", s.read(s.listUsers))
	mux.HandleFunc("POST /api/users/human", s.change(s.createHumanUser))
	mux.HandleFunc("POST /api/users/machine", s.change(s.createMachineUser))
	mux.HandleFunc("GET /api/users/{userId}", s.read(s.getUser))
	mux.HandleFunc("PATCH /api/users/{userId}", s.change(s.updateUser))
	mux.HandleFunc("DELETE /api/users/{userId}", s.change(s.deleteUser))
	mux.HandleFunc("POST /api/users/{userId}/password", s.change(s.setUserPassword))
	mux.HandleFunc("POST /api/users/{userId}/deactivate", s.change(s.deactivateUser))
	mux.HandleFunc("POST /api/users/{userId}/reactivate", s.change(s.reactivateUser))
	mux.HandleFunc("POST /api/users/{userId}/lock", s.change(s.lockUser))
	mux.HandleFunc("POST /api/users/{userId}/unlock", s.change(s.unlockUser))
	mux.HandleFunc("POST /api/users/{userId}/secret", s.change(s.generateUserSecret))
	mux.HandleFunc("DELETE /api/users/{userId}/secret", s.change(s.removeUserSecret))
	mux.HandleFunc("GET /api/users/{userId}/authorizations", s.read(s.listUserAuthorizations))

	mux.HandleFunc("POST /api/authorizations", s.change(s.createAuthorization))
	mux.HandleFunc("PATCH /api/authorizations/{authorizationId}", s.change(s.updateAuthorization))
	mux.HandleFunc("DELETE /api/authorizations/{authorizationId}", s.change(s.deleteAuthorization))

	mux.HandleFunc("GET /api/projects", s.read(s.listProjects))
	mux.HandleFunc("GET /api/projects/{projectId}", s.read(s.getProject))
	mux.HandleFunc("GET /api/projects/{projectId}/roles", s.read(s.listProjectRoles))
	mux.HandleFunc("GET /api/projects/{projectId}/authorizations", s.read(s.listProjectAuthorizations))

	s.registerBranding(mux)
	s.registerTenants(mux)

	mux.HandleFunc("GET /api/", s.NotFoundHandler)
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

// Otherwise unknown /api/ paths would get the SPA's index.html.
func (s *Service) NotFoundHandler(w http.ResponseWriter, r *http.Request) {
	core.WriteError(w, http.StatusNotFound, "not found")
}

type handler func(w http.ResponseWriter, r *http.Request, admin core.Session)

func (s *Service) read(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin, ok := s.admin(w, r); ok {
			h(w, r, admin)
		}
	}
}

// Every change is logged: who, which route, which target, which status.
func (s *Service) change(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, ok := s.admin(w, r)
		if !ok {
			return
		}
		if err := s.checkCSRF(r); err != nil {
			s.Log.WarnContext(r.Context(), "cross-site request refused", "sub", admin.Subject, "action", r.Pattern, "err", err)
			core.WriteError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		rec := &auditWriter{ResponseWriter: w, status: http.StatusOK, target: pathTarget(r)}
		h(rec, r, admin)
		s.Log.InfoContext(r.Context(), "admin change",
			"actor", admin.Subject, "action", r.Pattern, "target", rec.target, "status", rec.status)
	}
}

// The SPA sends the browser to the login on 401.
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

type auditWriter struct {
	http.ResponseWriter
	status int
	target string
}

func (a *auditWriter) WriteHeader(code int) {
	a.status = code
	a.ResponseWriter.WriteHeader(code)
}

// "instance" for the platform branding.
func pathTarget(r *http.Request) string {
	for _, name := range []string{"authorizationId", "userId", "orgId", "tenantId"} {
		if id := r.PathValue(name); id != "" {
			return id
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/platform/") {
		return "instance"
	}
	return ""
}

// The ID also becomes the target in the change log.
func created(w http.ResponseWriter, id string) {
	if a, ok := w.(*auditWriter); ok {
		a.target = id
	}
	core.WriteJSON(w, http.StatusCreated, Created{Id: id})
}

func noContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
