package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
)

type fakeSessions struct {
	session core.Session
	ok      bool
}

func (f fakeSessions) Session(*http.Request) (core.Session, bool) { return f.session, f.ok }

func me(sessions fakeSessions) *httptest.ResponseRecorder {
	svc := NewService(sessions, "super-admin", slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	svc.MeHandler(rec, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	return rec
}

func TestMeWithoutSessionIs401(t *testing.T) {
	rec := me(fakeSessions{})
	var body Error
	if rec.Code != http.StatusUnauthorized || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" {
		t.Fatalf("status %d, body %s; want 401 with an error", rec.Code, rec.Body)
	}
}

func TestMeWithoutAdminRoleIs403(t *testing.T) {
	rec := me(fakeSessions{ok: true, session: core.Session{Subject: "42", Roles: []string{"viewer"}}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

func TestMeReturnsTheSession(t *testing.T) {
	expires := time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)
	rec := me(fakeSessions{ok: true, session: core.Session{
		Subject: "7", Name: "Super Admin", Email: "admin@example.test", Roles: []string{"super-admin"}, Expires: expires,
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	var got Me
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Subject != "7" || got.Name != "Super Admin" || got.Email != "admin@example.test" ||
		len(got.Roles) != 1 || got.AdminRole != "super-admin" || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("got %+v", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the response may be cached")
	}
}
