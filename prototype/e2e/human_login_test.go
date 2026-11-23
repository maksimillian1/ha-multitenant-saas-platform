package e2e

// Browser sign-in of a human user through Login V2, without a browser: the
// test follows the OIDC redirects itself and does on Zitadel's session API
// what Login V2 does after the user typed the password.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// noRedirect is the e2e client that returns redirects instead of following them.
func noRedirect() *http.Client {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// oidcClient is an OIDC client that signs users in with the code flow and PKCE.
type oidcClient struct {
	ClientID    string
	RedirectURI string
}

// consoleClient reads the Console's OIDC client from the Console's own config.
func consoleClient(t testing.TB) oidcClient {
	t.Helper()
	resp, err := client.Get(cfg.Issuer + "/ui/console/assets/environment.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env struct {
		ClientID string `json:"clientid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || env.ClientID == "" {
		t.Fatalf("Console environment.json: no clientid (status %d, %v)", resp.StatusCode, err)
	}
	return oidcClient{ClientID: env.ClientID, RedirectURI: cfg.Issuer + "/ui/console/auth/callback"}
}

// startLogin starts the code flow and returns the auth request ID that Login V2
// got, after checking that Zitadel sent the browser to Login V2 and that its
// page renders.
func startLogin(t testing.TB, c oidcClient, scope, verifier string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id": {c.ClientID}, "redirect_uri": {c.RedirectURI}, "response_type": {"code"},
		"scope": {scope}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	resp, err := noRedirect().Get(cfg.Issuer + "/oauth/v2/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || err != nil || !strings.HasPrefix(loc.Path, "/ui/v2/login") {
		t.Fatalf("authorize: status %d, Location %q; want a redirect to Login V2", resp.StatusCode, resp.Header.Get("Location"))
	}
	authRequest := loc.Query().Get("authRequest")
	if authRequest == "" {
		t.Fatalf("Login V2 redirect %q has no authRequest", loc)
	}
	page, err := client.Get(cfg.Issuer + loc.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("Login V2 page %s: status %d; body %.300s", loc.Path, page.StatusCode, body)
	}
	return authRequest
}

// humanToken signs a human user in through Login V2 and returns the access token.
// iamAdmin stands in for Login V2's own service user on the session API.
func humanToken(t testing.TB, iamAdmin string, c oidcClient, scope, loginName, password string) string {
	t.Helper()
	verifier := randomID() + randomID() + randomID() + randomID() + randomID() + randomID() + randomID() + randomID()
	authRequest := startLogin(t, c, scope, verifier)

	r := zitadelCall(t, iamAdmin, http.MethodPost, "/v2/sessions", map[string]any{
		"checks": map[string]any{"user": map[string]string{"loginName": loginName}, "password": map[string]string{"password": password}},
	})
	mustStatus(t, r, http.StatusCreated, "create a session with the password")
	var session struct {
		SessionID    string `json:"sessionId"`
		SessionToken string `json:"sessionToken"`
	}
	r.decode(t, &session)

	r = zitadelCall(t, iamAdmin, http.MethodPost, "/v2/oidc/auth_requests/"+authRequest, map[string]any{
		"session": map[string]string{"sessionId": session.SessionID, "sessionToken": session.SessionToken},
	})
	mustStatus(t, r, http.StatusOK, "finish the auth request with the session")
	var cb struct {
		CallbackURL string `json:"callbackUrl"`
	}
	r.decode(t, &cb)
	u, err := url.Parse(cb.CallbackURL)
	if err != nil || u.Query().Get("code") == "" {
		t.Fatalf("callback %q has no code", cb.CallbackURL)
	}

	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "redirect_uri": {c.RedirectURI},
		"client_id": {c.ClientID}, "code_verifier": {verifier},
	}
	resp, err := client.PostForm(cfg.Issuer+"/oauth/v2/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token for %s: %d %s", loginName, resp.StatusCode, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		t.Fatalf("token for %s: no access_token", loginName)
	}
	return tok.AccessToken
}

// invitedHuman creates a human user in orgID as an admin would, by invitation:
// the user verifies the invite code and sets its password, as on Login V2's
// invite page. It returns the login name and the password.
func invitedHuman(t testing.TB, iamAdmin, creator, orgID string) (userID, loginName, password string) {
	t.Helper()
	name := "e2e-human-" + randomID()
	r := zitadelCall(t, creator, http.MethodPost, "/v2/users/human", map[string]any{
		"organization": map[string]string{"orgId": orgID},
		"username":     name,
		"profile":      map[string]string{"givenName": "E2E", "familyName": "Human"},
		"email":        map[string]any{"email": name + "@example.invalid", "isVerified": true},
	})
	mustStatus(t, r, http.StatusOK, "create a human user")
	var user struct {
		UserID string `json:"userId"`
	}
	r.decode(t, &user)
	t.Cleanup(func() {
		zitadelCall(t, iamAdmin, http.MethodDelete, "/v2/users/"+user.UserID, nil)
	})

	r = zitadelCall(t, creator, http.MethodPost, "/v2/users/"+user.UserID+"/invite_code", map[string]any{"returnCode": map[string]any{}})
	mustStatus(t, r, http.StatusOK, "create the invite code")
	var invite struct {
		InviteCode string `json:"inviteCode"`
	}
	r.decode(t, &invite)
	mustStatus(t, zitadelCall(t, iamAdmin, http.MethodPost, "/v2/users/"+user.UserID+"/invite_code/_verify",
		map[string]string{"verificationCode": invite.InviteCode}), http.StatusOK, "verify the invite code")

	password = "E2e-" + randomID() + "-Pw1!"
	mustStatus(t, zitadelCall(t, iamAdmin, http.MethodPost, "/v2/users/"+user.UserID+"/password", map[string]any{
		"newPassword": map[string]any{"password": password, "changeRequired": false},
	}), http.StatusOK, "set the password")
	return user.UserID, name, password
}
