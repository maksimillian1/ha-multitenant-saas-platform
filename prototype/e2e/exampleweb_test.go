package e2e

// The tenant web page of ExampleApp (apps/exampleweb), driven like a browser:
// a cookie jar, the redirects followed by hand, and Login V2's part done on
// Zitadel's session API.

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

const exampleWebURL = "http://exampleweb.localtest.me:30080"

type browser struct {
	t      *testing.T
	client *http.Client
}

func newBrowser(t *testing.T) *browser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := noRedirect()
	c.Jar = jar
	return &browser{t: t, client: c}
}

func (b *browser) get(rawURL string) (*http.Response, string) {
	b.t.Helper()
	resp, err := b.client.Get(rawURL)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// signIn opens the page, follows the redirects to Login V2, signs the user in
// there and follows the callback back to the page; it returns the page's HTML.
func (b *browser) signIn(loginName, password string) string {
	b.t.Helper()
	next := exampleWebURL + "/"
	for range 5 {
		resp, _ := b.get(next)
		if resp.StatusCode != http.StatusFound {
			b.t.Fatalf("GET %s: status %d, want a redirect towards Login V2", next, resp.StatusCode)
		}
		loc, err := resp.Location()
		if err != nil {
			b.t.Fatal(err)
		}
		if strings.HasPrefix(loc.Path, "/ui/v2/login") {
			callback := finishAuthRequest(b.t, loc.Query().Get("authRequest"), loginName, password)
			if !strings.HasPrefix(callback.String(), exampleWebURL+"/auth/callback") {
				b.t.Fatalf("callback %s is not exampleweb's", callback)
			}
			resp, body := b.get(callback.String())
			if resp.StatusCode != http.StatusFound {
				b.t.Fatalf("callback: status %d, want 302 to the page; body %.300s", resp.StatusCode, body)
			}
			resp, body = b.get(exampleWebURL + "/")
			if resp.StatusCode != http.StatusOK {
				b.t.Fatalf("page after sign-in: status %d; body %.300s", resp.StatusCode, body)
			}
			return body
		}
		next = loc.String()
	}
	b.t.Fatal("no redirect to Login V2 within 5 hops")
	return ""
}

func (b *browser) cookieValues() []string {
	u, _ := url.Parse(exampleWebURL)
	var out []string
	for _, c := range b.client.Jar.Cookies(u) {
		out = append(out, c.Value)
	}
	return out
}

// Verifies: tenant-self-admin AC-8; T-01, T-12
func TestExampleWebShowsOnlyOwnItems(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	itemA := createItem(t, token(t, tenA), "web-a-"+randomID())
	itemB := createItem(t, token(t, tenB), "web-b-"+randomID())

	_, loginA, passwordA := invitedHuman(t, iamAdmin, iamAdmin, tenA.OrgID)
	pageA := newBrowser(t).signIn(loginA, passwordA)
	if !strings.Contains(pageA, itemA.ID) || strings.Contains(pageA, itemB.ID) {
		t.Errorf("tenant A's user: page has A's item %v and B's item %v; want only A's",
			strings.Contains(pageA, itemA.ID), strings.Contains(pageA, itemB.ID))
	}

	// Canary: tenant B's user sees the other set on the same page, so the page
	// reads with the user's own token and not with one fixed tenant's.
	_, loginB, passwordB := invitedHuman(t, iamAdmin, iamAdmin, tenB.OrgID)
	pageB := newBrowser(t).signIn(loginB, passwordB)
	if !strings.Contains(pageB, itemB.ID) || strings.Contains(pageB, itemA.ID) {
		t.Errorf("tenant B's user: page has B's item %v and A's item %v; want only B's",
			strings.Contains(pageB, itemB.ID), strings.Contains(pageB, itemA.ID))
	}
	if strings.Contains(pageA, `id="manage-users"`) {
		t.Error("a user without ORG_OWNER sees the Manage users link")
	}
}

// Verifies: tenant-self-admin AC-7, AC-9, AC-10
func TestExampleWebOrgOwnerLinkAndLogout(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	userID, loginName, password := invitedHuman(t, iamAdmin, iamAdmin, tenA.OrgID)
	mustStatus(t, zitadelOrgCall(t, iamAdmin, tenA.OrgID, http.MethodPost, "/management/v1/orgs/me/members",
		map[string]any{"userId": userID, "roles": []string{"ORG_OWNER"}}), http.StatusOK, "make the user ORG_OWNER")

	b := newBrowser(t)
	page := b.signIn(loginName, password)
	if !strings.Contains(page, `id="manage-users" href="`+cfg.Issuer+`/ui/console"`) {
		t.Errorf("ORG_OWNER's page has no Manage users link to the Console; page %.500s", page)
	}
	for _, v := range b.cookieValues() {
		if strings.Count(v, ".") == 2 && len(v) > 100 {
			t.Errorf("the browser holds a JWT-shaped cookie value")
		}
	}

	req, _ := http.NewRequest(http.MethodPost, exampleWebURL+"/auth/logout", nil)
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := resp.Location()
	if resp.StatusCode != http.StatusFound || loc == nil || !strings.HasSuffix(loc.Path, "/oidc/v1/end_session") {
		t.Errorf("logout: status %d to %v, want 302 to Zitadel's end_session", resp.StatusCode, loc)
	}
	resp, _ = b.get(exampleWebURL + "/")
	if l, _ := resp.Location(); resp.StatusCode != http.StatusFound || l == nil || l.Path != "/auth/login" {
		t.Errorf("page after logout: status %d to %v, want 302 to /auth/login", resp.StatusCode, l)
	}
}
