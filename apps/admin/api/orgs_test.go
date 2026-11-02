package api

import (
	"net/http"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

func TestListOrgs(t *testing.T) {
	e := newEnv(t)
	var list OrgList
	expect(t, e.do("GET", "/api/orgs", nil), http.StatusOK, &list)
	if list.Total != 3 || len(list.Items) != 3 {
		t.Fatalf("got %+v", list)
	}
	zitadelOrg, tenantB := list.Items[0], list.Items[2]
	if zitadelOrg.Name != "ZITADEL" || !zitadelOrg.IsDefault || zitadelOrg.State != OrgStateActive ||
		zitadelOrg.CreatedAt == nil || !zitadelOrg.CreatedAt.Equal(created1) || zitadelOrg.ChangedAt != nil {
		t.Errorf("default organization = %+v", zitadelOrg)
	}
	if tenantB.Name != "tenant-b" || tenantB.IsDefault || tenantB.State != OrgStateInactive || tenantB.PrimaryDomain != "tenant-b.localtest.me" {
		t.Errorf("tenant-b = %+v", tenantB)
	}
	if e.z.page != (zitadel.Page{Limit: 20}) {
		t.Errorf("default page = %+v, want limit 20", e.z.page)
	}

	expect(t, e.do("GET", "/api/orgs?search=+TENANT+&state=active&offset=0&limit=10", nil), http.StatusOK, &list)
	if e.z.orgFilter != (zitadel.OrganizationFilter{Name: "TENANT", State: zitadel.OrganizationActive}) || e.z.page != (zitadel.Page{Limit: 10}) {
		t.Errorf("filter %+v, page %+v", e.z.orgFilter, e.z.page)
	}
	if list.Total != 1 || list.Items[0].Id != "org-a" {
		t.Errorf("active tenants = %+v", list)
	}
	expect(t, e.do("GET", "/api/orgs?state=inactive&offset=1", nil), http.StatusOK, &list)
	if e.z.orgFilter.State != zitadel.OrganizationInactive || e.z.page.Offset != 1 || list.Total != 1 || len(list.Items) != 0 {
		t.Errorf("filter %+v, page %+v, list %+v", e.z.orgFilter, e.z.page, list)
	}
}

func TestOrgLifecycle(t *testing.T) {
	e := newEnv(t)
	var made Created
	expect(t, e.do("POST", "/api/orgs", OrgCreate{Name: "  tenant-c "}), http.StatusCreated, &made)
	if made.Id == "" || e.z.orgs[made.Id].Name != "tenant-c" {
		t.Fatalf("created %+v, stored %+v", made, e.z.orgs[made.Id])
	}
	path := "/api/orgs/" + made.Id

	var org Org
	expect(t, e.do("GET", path, nil), http.StatusOK, &org)
	if org.Id != made.Id || org.Name != "tenant-c" || org.State != OrgStateActive || org.IsDefault {
		t.Fatalf("org = %+v", org)
	}
	expect(t, e.do("PATCH", path, OrgUpdate{Name: "tenant-d"}), http.StatusNoContent, nil)
	expect(t, e.do("POST", path+"/deactivate", nil), http.StatusNoContent, nil)
	expect(t, e.do("GET", path, nil), http.StatusOK, &org)
	if org.Name != "tenant-d" || org.State != OrgStateInactive {
		t.Fatalf("after rename and deactivate: %+v", org)
	}
	expect(t, e.do("POST", path+"/activate", nil), http.StatusNoContent, nil)
	if e.z.orgs[made.Id].State != zitadel.OrganizationActive {
		t.Fatal("not activated")
	}
	expect(t, e.do("DELETE", path, nil), http.StatusNoContent, nil)
	if rec := e.do("GET", path, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted organization: status %d", rec.Code)
	}
}

func TestUnknownOrgIs404(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/orgs/nope"},
		{"POST", "/api/orgs/nope/activate"},
		{"POST", "/api/orgs/nope/deactivate"},
		{"DELETE", "/api/orgs/nope"},
	} {
		if rec := e.do(tc.method, tc.path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}
