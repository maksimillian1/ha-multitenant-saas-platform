package api

import (
	"cmp"
	"net/http"
	"sync"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

// overview counts organizations, humans, machines and projects. Each count
// is the total of a list call for one item, made in parallel.
func (s *Service) overview(w http.ResponseWriter, r *http.Request, _ core.Session) {
	ctx := r.Context()
	one := zitadel.Page{Limit: 1}
	var (
		orgs                   zitadel.OrganizationList
		humans, machines       zitadel.UserList
		projects               zitadel.ProjectList
		orgErr, humanErr       error
		machineErr, projectErr error
		wg                     sync.WaitGroup
	)
	wg.Go(func() { orgs, orgErr = s.Zitadel.ListOrganizations(ctx, zitadel.OrganizationFilter{}, one) })
	wg.Go(func() { humans, humanErr = s.Zitadel.ListUsers(ctx, zitadel.UserFilter{Type: zitadel.TypeHuman}, one) })
	wg.Go(func() {
		machines, machineErr = s.Zitadel.ListUsers(ctx, zitadel.UserFilter{Type: zitadel.TypeMachine}, one)
	})
	wg.Go(func() { projects, projectErr = s.Zitadel.ListProjects(ctx, zitadel.ProjectFilter{}, one) })
	wg.Wait()
	if err := cmp.Or(orgErr, humanErr, machineErr, projectErr); err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, Overview{
		Organizations: total(orgs.Total),
		HumanUsers:    total(humans.Total),
		MachineUsers:  total(machines.Total),
		Projects:      total(projects.Total),
	})
}
