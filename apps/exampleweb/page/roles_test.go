package page

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func membershipsServer(t *testing.T, status int, body string) *Memberships {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/v1/memberships/me/_search" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request %s %s with %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Memberships{ZitadelURL: srv.URL, HTTP: srv.Client()}
}

// Verifies: tenant-self-admin AC-9; T-02
func TestMembershipsDecodeZitadelAnswer(t *testing.T) {
	// Shape of Zitadel v4's ListMyMemberships answer.
	answer := `{"details":{"totalResult":"2"},"result":[
		{"userId":"u","roles":["PROJECT_OWNER"],"projectId":"p"},
		{"userId":"u","roles":["ORG_USER_MANAGER","ORG_OWNER"],"orgId":"org-a","displayName":"tenant-a"}]}`
	for _, tc := range []struct {
		org  string
		want bool
	}{{"org-a", true}, {"org-b", false}} {
		got, err := membershipsServer(t, http.StatusOK, answer).IsOrgOwner(context.Background(), "tok", tc.org)
		if err != nil || got != tc.want {
			t.Errorf("IsOrgOwner(%s) = %v, %v; want %v", tc.org, got, err, tc.want)
		}
	}
}

// Verifies: T-02
func TestMembershipsBrokenAnswerIsAnError(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{http.StatusOK, `{"result":`}, {http.StatusForbidden, `{}`}} {
		got, err := membershipsServer(t, tc.status, tc.body).IsOrgOwner(context.Background(), "tok", "org-a")
		if err == nil || got {
			t.Errorf("status %d body %q: got %v, %v; want an error and no role", tc.status, tc.body, got, err)
		}
	}
}
