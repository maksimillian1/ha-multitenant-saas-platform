package api

import (
	"net/http"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

func TestOverviewCounts(t *testing.T) {
	e := newEnv(t)
	var got Overview
	expect(t, e.do("GET", "/api/overview", nil), http.StatusOK, &got)
	if got != (Overview{Organizations: 3, HumanUsers: 2, MachineUsers: 2, Projects: 2}) {
		t.Fatalf("overview = %+v", got)
	}

	e.z.err = &zitadel.Error{Status: 503, Code: zitadel.CodeUnavailable}
	if rec := e.do("GET", "/api/overview", nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("with Zitadel down: status %d, want 502", rec.Code)
	}
}
