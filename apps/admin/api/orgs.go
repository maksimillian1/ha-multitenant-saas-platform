package api

import (
	"cmp"
	"errors"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

func (s *Service) listOrgs(w http.ResponseWriter, r *http.Request, _ core.Session) {
	q := r.URL.Query()
	p, err := page(q)
	if err != nil {
		badRequest(w, err)
		return
	}
	filter := zitadel.OrganizationFilter{Name: strings.TrimSpace(q.Get("search"))}
	if utf8.RuneCountInString(filter.Name) > 200 {
		badRequest(w, errors.New("search has more than 200 characters"))
		return
	}
	switch OrgState(q.Get("state")) {
	case "":
	case OrgStateActive:
		filter.State = zitadel.OrganizationActive
	case OrgStateInactive:
		filter.State = zitadel.OrganizationInactive
	default:
		badRequest(w, errors.New("state must be active or inactive"))
		return
	}

	var (
		list             zitadel.OrganizationList
		defaultID        string
		listErr, deflErr error
		wg               sync.WaitGroup
	)
	wg.Go(func() { list, listErr = s.Zitadel.ListOrganizations(r.Context(), filter, p) })
	wg.Go(func() { defaultID, deflErr = s.Zitadel.DefaultOrganizationID(r.Context()) })
	wg.Wait()
	if err := cmp.Or(listErr, deflErr); err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]Org, 0, len(list.Organizations))
	for _, o := range list.Organizations {
		items = append(items, orgFromZitadel(o, defaultID))
	}
	core.WriteJSON(w, http.StatusOK, OrgList{Items: items, Total: total(list.Total)})
}

func (s *Service) getOrg(w http.ResponseWriter, r *http.Request, _ core.Session) {
	var (
		org             zitadel.Organization
		defaultID       string
		getErr, deflErr error
		wg              sync.WaitGroup
	)
	wg.Go(func() { org, getErr = s.Zitadel.GetOrganization(r.Context(), r.PathValue("orgId")) })
	wg.Go(func() { defaultID, deflErr = s.Zitadel.DefaultOrganizationID(r.Context()) })
	wg.Wait()
	if err := cmp.Or(getErr, deflErr); err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, orgFromZitadel(org, defaultID))
}

func (s *Service) updateOrg(w http.ResponseWriter, r *http.Request, _ core.Session) {
	var body OrgUpdate
	if !decode(w, r, &body) {
		return
	}
	var f fields
	f.text("name", &body.Name, 1, 200)
	if err := f.err(); err != nil {
		badRequest(w, err)
		return
	}
	if err := s.Zitadel.UpdateOrganization(r.Context(), r.PathValue("orgId"), body.Name); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}

func (s *Service) deactivateOrg(w http.ResponseWriter, r *http.Request, admin core.Session) {
	id := r.PathValue("orgId")
	if err := s.checkOrgChange(r.Context(), admin, id, "deactivate"); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Zitadel.DeactivateOrganization(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}

func (s *Service) activateOrg(w http.ResponseWriter, r *http.Request, _ core.Session) {
	if err := s.Zitadel.ActivateOrganization(r.Context(), r.PathValue("orgId")); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}
