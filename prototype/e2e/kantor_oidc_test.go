package e2e

import (
	"net/http"
	"testing"
)

// Verifies: kantor-oidc AC-1, AC-2; T-01
func TestKantorSPASignInIsAcceptedByTheGateway(t *testing.T) {
	if cfg.Kantor.ClientID == "" {
		t.Fatal("no kantor client in .tenants.json; run bootstrap-tenants.sh")
	}
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	_, login, password := invitedHuman(t, iamAdmin, iamAdmin, tenA.OrgID)

	// The token request carries no client secret: the SPA is a public client with PKCE.
	spa := oidcClient{ClientID: cfg.Kantor.ClientID, RedirectURI: cfg.Kantor.URL + "/auth/callback"}
	tok := humanToken(t, "", spa, tokenScope(cfg.Audience)+" offline_access", login, password)

	_, _, _, claims := jwtParts(t, tok)
	if !hasAudience(claims["aud"], cfg.Audience) {
		t.Fatalf("aud = %v, want the platform API project %s", claims["aud"], cfg.Audience)
	}
	if got := claims["urn:zitadel:iam:user:resourceowner:id"]; got != tenA.OrgID {
		t.Fatalf("resource owner = %v, want tenant A %s", got, tenA.OrgID)
	}

	itemA := createItem(t, token(t, tenA), "kantor-oidc-a-"+randomID())
	itemB := createItem(t, token(t, tenB), "kantor-oidc-b-"+randomID())
	items := listItems(t, tok)
	if !containsID(items, itemA.ID) || containsID(items, itemB.ID) {
		t.Fatalf("KANTOR user of tenant A: has A's item %v, B's item %v; want only A's",
			containsID(items, itemA.ID), containsID(items, itemB.ID))
	}
}

// Verifies: kantor-oidc AC-3; T-12
func TestKantorClientRefusesForeignRedirect(t *testing.T) {
	if cfg.Kantor.ClientID == "" {
		t.Fatal("no kantor client in .tenants.json; run bootstrap-tenants.sh")
	}
	resp, err := noRedirect().Get(cfg.Issuer + "/oauth/v2/authorize?response_type=code&scope=openid" +
		"&code_challenge=" + randomID() + randomID() + randomID() + randomID() + "&code_challenge_method=S256" +
		"&client_id=" + cfg.Kantor.ClientID + "&redirect_uri=http://evil.localtest.me:30080/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode == http.StatusFound && len(loc) >= 24 && loc[:24] == "http://evil.localtest.me" {
		t.Fatalf("authorize with a foreign redirect_uri sent the browser to %s", loc)
	}
}
