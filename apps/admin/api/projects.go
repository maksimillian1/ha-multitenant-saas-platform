package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

func (s *Service) listProjects(w http.ResponseWriter, r *http.Request, _ core.Session) {
	q := r.URL.Query()
	p, err := page(q)
	if err != nil {
		badRequest(w, err)
		return
	}
	filter := zitadel.ProjectFilter{Name: strings.TrimSpace(q.Get("search")), OrganizationID: strings.TrimSpace(q.Get("org_id"))}
	if utf8.RuneCountInString(filter.Name) > 200 || len(filter.OrganizationID) > 200 {
		badRequest(w, errors.New("search and org_id have at most 200 characters"))
		return
	}
	list, err := s.Zitadel.ListProjects(r.Context(), filter, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	orgIDs := make([]string, 0, len(list.Projects))
	for _, project := range list.Projects {
		orgIDs = append(orgIDs, project.OrganizationID)
	}
	names := s.orgNames(r.Context(), orgIDs...)
	items := make([]Project, 0, len(list.Projects))
	for _, project := range list.Projects {
		items = append(items, projectFromZitadel(project, names))
	}
	core.WriteJSON(w, http.StatusOK, ProjectList{Items: items, Total: total(list.Total)})
}

func (s *Service) getProject(w http.ResponseWriter, r *http.Request, _ core.Session) {
	project, err := s.Zitadel.GetProject(r.Context(), r.PathValue("projectId"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, projectFromZitadel(project, s.orgNames(r.Context(), project.OrganizationID)))
}

func (s *Service) listProjectRoles(w http.ResponseWriter, r *http.Request, _ core.Session) {
	roles, err := s.Zitadel.ListProjectRoles(r.Context(), r.PathValue("projectId"), zitadel.Page{Limit: maxRelated})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]ProjectRole, 0, len(roles))
	for _, role := range roles {
		items = append(items, ProjectRole{Key: role.Key, DisplayName: role.DisplayName, Group: role.Group})
	}
	core.WriteJSON(w, http.StatusOK, ProjectRoleList{Items: items})
}

func (s *Service) listProjectAuthorizations(w http.ResponseWriter, r *http.Request, _ core.Session) {
	list, err := s.Zitadel.ListAuthorizations(r.Context(), zitadel.AuthorizationFilter{ProjectID: r.PathValue("projectId")}, zitadel.Page{Limit: maxRelated})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, authorizationList(list))
}
