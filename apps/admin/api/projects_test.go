package api

import (
	"net/http"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

func TestProjects(t *testing.T) {
	e := newEnv(t)
	var list ProjectList
	expect(t, e.do("GET", "/api/projects?search=platform&org_id=org-zitadel&limit=50", nil), http.StatusOK, &list)
	if e.z.projectFilter != (zitadel.ProjectFilter{Name: "platform", OrganizationID: "org-zitadel"}) || e.z.page != (zitadel.Page{Limit: 50}) {
		t.Fatalf("filter %+v, page %+v", e.z.projectFilter, e.z.page)
	}
	if list.Total != 2 || list.Items[0].Name != "platform-admin" || list.Items[0].OrgName != "ZITADEL" ||
		!list.Items[0].RoleAssertion || !list.Items[0].AuthorizationRequired || list.Items[0].AccessRequired ||
		list.Items[0].State != ProjectStateActive || list.Items[0].GrantedOrgId != nil {
		t.Fatalf("projects = %+v", list)
	}

	var project Project
	expect(t, e.do("GET", "/api/projects/p-admin", nil), http.StatusOK, &project)
	if project.Id != "p-admin" || project.OrgName != "ZITADEL" || project.CreatedAt == nil {
		t.Fatalf("project = %+v", project)
	}
	if rec := e.do("GET", "/api/projects/nope", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project: status %d", rec.Code)
	}

	var roles ProjectRoleList
	expect(t, e.do("GET", "/api/projects/p-admin/roles", nil), http.StatusOK, &roles)
	if len(roles.Items) != 2 || roles.Items[0].Key != "super-admin" || roles.Items[1].Key != "viewer" {
		t.Fatalf("roles = %+v", roles)
	}

	var holders AuthorizationList
	expect(t, e.do("GET", "/api/projects/p-admin/authorizations", nil), http.StatusOK, &holders)
	if holders.Total != 2 || holders.Items[0].UserId != "admin" || holders.Items[1].UserId != "ann" {
		t.Fatalf("holders = %+v", holders)
	}
}

func TestOrgNameThatCannotBeReadStaysEmpty(t *testing.T) {
	e := newEnv(t)
	e.z.projects["p-orphan"] = &zitadel.Project{ID: "p-orphan", OrganizationID: "org-gone", Name: "orphan"}
	var project Project
	expect(t, e.do("GET", "/api/projects/p-orphan", nil), http.StatusOK, &project)
	if project.OrgId != "org-gone" || project.OrgName != "" {
		t.Fatalf("project = %+v", project)
	}
}
