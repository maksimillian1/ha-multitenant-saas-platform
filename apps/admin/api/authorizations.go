package api

import (
	"net/http"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
)

func (s *Service) createAuthorization(w http.ResponseWriter, r *http.Request, _ core.Session) {
	var body AuthorizationCreate
	if !decode(w, r, &body) {
		return
	}
	var f fields
	f.text("user_id", &body.UserId, 1, 200)
	f.text("project_id", &body.ProjectId, 1, 200)
	f.optionalText("org_id", body.OrgId, 200, true)
	f.roleKeys(&body.RoleKeys)
	if err := f.err(); err != nil {
		badRequest(w, err)
		return
	}
	orgID := stringValue(body.OrgId)
	if orgID == "" {
		// The roles are given on behalf of the organization that owns the
		// project.
		project, err := s.Zitadel.GetProject(r.Context(), body.ProjectId)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		orgID = project.OrganizationID
	}
	id, err := s.Zitadel.CreateAuthorization(r.Context(), body.UserId, body.ProjectId, orgID, body.RoleKeys)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	created(w, id)
}

func (s *Service) updateAuthorization(w http.ResponseWriter, r *http.Request, admin core.Session) {
	var body AuthorizationUpdate
	if !decode(w, r, &body) {
		return
	}
	var f fields
	f.roleKeys(&body.RoleKeys)
	if err := f.err(); err != nil {
		badRequest(w, err)
		return
	}
	id := r.PathValue("authorizationId")
	if err := s.checkAuthorizationChange(r.Context(), admin, id); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Zitadel.UpdateAuthorization(r.Context(), id, body.RoleKeys); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}

func (s *Service) deleteAuthorization(w http.ResponseWriter, r *http.Request, admin core.Session) {
	id := r.PathValue("authorizationId")
	if err := s.checkAuthorizationChange(r.Context(), admin, id); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Zitadel.DeleteAuthorization(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}
