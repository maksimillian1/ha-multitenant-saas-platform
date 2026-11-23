package page

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/items"
)

type fakeSessions struct {
	session    core.Session
	ok         bool
	refreshErr error
	refreshes  int
}

func (f *fakeSessions) Session(*http.Request) (string, core.Session, error) {
	if !f.ok {
		return "", core.Session{}, errors.New("no session")
	}
	return "sid", f.session, nil
}

func (f *fakeSessions) Refresh(_ context.Context, _ string, s core.Session) (core.Session, error) {
	f.refreshes++
	if f.refreshErr != nil {
		return core.Session{}, f.refreshErr
	}
	s.AccessToken = "fresh"
	return s, nil
}

// fakeItems accepts only the token "fresh" when expired is set.
type fakeItems struct {
	expired bool
	tokens  []string
}

func (f *fakeItems) List(_ context.Context, token string) ([]items.Item, error) {
	f.tokens = append(f.tokens, token)
	if f.expired && token != "fresh" {
		return nil, items.ErrUnauthorized
	}
	return []items.Item{{ID: "1", Name: "first <item>"}}, nil
}

type fakeOwners struct {
	owner bool
	err   error
	orgs  []string
}

func (f *fakeOwners) IsOrgOwner(_ context.Context, _, orgID string) (bool, error) {
	f.orgs = append(f.orgs, orgID)
	return f.owner, f.err
}

func newPage(s *fakeSessions, i *fakeItems, o *fakeOwners) *Service {
	return &Service{Sessions: s, Items: i, Owners: o, ConsoleURL: "http://zitadel.test/ui/console",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func get(s *Service) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.Handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}

func signedIn() *fakeSessions {
	return &fakeSessions{ok: true, session: core.Session{Name: "Alice", OrgID: "org-a", AccessToken: "old", RefreshToken: "r"}}
}

// Verifies: tenant-self-admin AC-7
func TestPageWithoutSessionRedirectsToLogin(t *testing.T) {
	i := &fakeItems{}
	rec := get(newPage(&fakeSessions{}, i, &fakeOwners{}))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/auth/login" {
		t.Errorf("no session: status %d to %q, want 302 to /auth/login", rec.Code, rec.Header().Get("Location"))
	}
	if len(i.tokens) != 0 {
		t.Error("items were read without a session")
	}
}

// Verifies: tenant-self-admin AC-9
func TestManageUsersLinkOnlyForOrgOwner(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner *fakeOwners
		link  bool
	}{
		{"org owner", &fakeOwners{owner: true}, true},
		{"tenant user", &fakeOwners{}, false},
		{"memberships unavailable", &fakeOwners{owner: false, err: errors.New("down")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(newPage(signedIn(), &fakeItems{}, tc.owner))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if got := strings.Contains(body, `href="http://zitadel.test/ui/console"`); got != tc.link {
				t.Errorf("Manage users link shown = %v, want %v", got, tc.link)
			}
			if len(tc.owner.orgs) != 1 || tc.owner.orgs[0] != "org-a" {
				t.Errorf("role checked for orgs %v, want the session's org-a", tc.owner.orgs)
			}
			if !strings.Contains(body, "first &lt;item&gt;") {
				t.Error("the item name is not shown HTML-escaped")
			}
		})
	}
}

// Verifies: tenant-self-admin AC-11
func TestRefreshOnceOn401(t *testing.T) {
	s, i := signedIn(), &fakeItems{expired: true}
	rec := get(newPage(s, i, &fakeOwners{}))
	if rec.Code != http.StatusOK || s.refreshes != 1 || strings.Join(i.tokens, ",") != "old,fresh" {
		t.Errorf("status %d, refreshes %d, tokens %v; want 200 after one refresh and a retry with the new token",
			rec.Code, s.refreshes, i.tokens)
	}

	s = signedIn()
	s.refreshErr = errors.New("refresh token expired")
	i = &fakeItems{expired: true}
	rec = get(newPage(s, i, &fakeOwners{}))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/auth/login" || len(i.tokens) != 1 {
		t.Errorf("failed refresh: status %d to %q after %d calls; want one call and 302 to /auth/login",
			rec.Code, rec.Header().Get("Location"), len(i.tokens))
	}
}
