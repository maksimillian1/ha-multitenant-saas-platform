package zitadel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"
)

// Skipped unless both are set:
//
//	ZITADEL_TEST_URL=http://zitadel.localtest.me:30080
//	ZITADEL_TEST_KEY_FILE=<key.json of Secret admin/admin-zitadel> (prototype/README.md)
func integrationClient(t *testing.T) (*Client, string) {
	t.Helper()
	base, keyFile := os.Getenv("ZITADEL_TEST_URL"), os.Getenv("ZITADEL_TEST_KEY_FILE")
	if base == "" || keyFile == "" {
		t.Skip("ZITADEL_TEST_URL and ZITADEL_TEST_KEY_FILE are not set")
	}
	key, err := LoadKey("ZITADEL_TEST_KEY_FILE", keyFile)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	// On kind the issuer is the URL the API is reached at.
	tokens, err := NewKeyTokens(context.Background(), base, key, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(base, tokens, httpClient), base
}

// Everything it creates is deleted again.
func TestIntegration(t *testing.T) {
	c, _ := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	suffix := randomSuffix(t)

	account, err := c.ServiceAccount(ctx)
	if err != nil || account.UserID == "" || account.OrganizationID == "" {
		t.Fatalf("service account = %+v, %v", account, err)
	}
	if defaultOrg, err := c.DefaultOrganizationID(ctx); err != nil || defaultOrg == "" {
		t.Fatalf("default organization = %q, %v", defaultOrg, err)
	}

	orgID, err := c.AddOrganization(ctx, "it-org-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.DeleteOrganization(context.Background(), orgID) })
	must(t, c.UpdateOrganization(ctx, orgID, "it-renamed-"+suffix))
	eventually(t, func() bool {
		org, err := c.GetOrganization(ctx, orgID)
		return err == nil && org.Name == "it-renamed-"+suffix && org.State == OrganizationActive
	})
	found, err := c.ListOrganizations(ctx, OrganizationFilter{Name: "renamed-" + suffix}, Page{Limit: 10})
	if err != nil || found.Total != 1 || found.Organizations[0].ID != orgID {
		t.Fatalf("search by name = %+v, %v", found, err)
	}
	must(t, c.DeactivateOrganization(ctx, orgID))
	eventually(t, func() bool {
		org, err := c.GetOrganization(ctx, orgID)
		return err == nil && org.State == OrganizationInactive
	})
	must(t, c.ActivateOrganization(ctx, orgID))

	humanID, err := c.CreateHumanUser(ctx, NewHumanUser{OrganizationID: orgID, Username: "it-human-" + suffix,
		GivenName: "Integration", FamilyName: "Test", Email: "it-" + suffix + "@example.test", EmailVerified: true,
		Password: "Pw1!" + suffix, PasswordChangeRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		u, err := c.GetUser(ctx, humanID)
		return err == nil && u.Human != nil && u.Details.ResourceOwner == orgID && u.Human.Email.IsVerified
	})
	users, err := c.ListUsers(ctx, UserFilter{Search: "it-human-" + suffix, OrganizationID: orgID, Type: TypeHuman}, Page{Limit: 10})
	if err != nil || users.Total != 1 || users.Users[0].UserID != humanID {
		t.Fatalf("search users = %+v, %v", users, err)
	}
	nick := "nick-" + suffix
	must(t, c.UpdateUser(ctx, humanID, UserUpdate{Human: &HumanUpdate{NickName: &nick}}))
	must(t, c.SetPassword(ctx, humanID, "Pw2!"+suffix, false))
	must(t, c.LockUser(ctx, humanID))
	eventually(t, func() bool { u, err := c.GetUser(ctx, humanID); return err == nil && u.State == UserLocked })
	must(t, c.UnlockUser(ctx, humanID))
	must(t, c.DeactivateUser(ctx, humanID))
	eventually(t, func() bool { u, err := c.GetUser(ctx, humanID); return err == nil && u.State == UserInactive })
	must(t, c.ReactivateUser(ctx, humanID))

	machineID, err := c.CreateMachineUser(ctx, NewMachineUser{OrganizationID: orgID, Username: "it-machine-" + suffix,
		Name: "Integration machine", AccessTokenType: AccessTokenJWT})
	if err != nil {
		t.Fatal(err)
	}
	if secret, err := c.AddSecret(ctx, machineID); err != nil || secret == "" {
		t.Fatalf("secret = %q, %v", secret, err)
	}
	eventually(t, func() bool {
		u, err := c.GetUser(ctx, machineID)
		return err == nil && u.Machine != nil && u.Machine.HasSecret
	})
	must(t, c.RemoveSecret(ctx, machineID))

	// A role assignment in the admin app's own project, if it exists.
	projects, err := c.ListProjects(ctx, ProjectFilter{Name: "platform-admin"}, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(projects.Projects, func(p Project) bool { return p.Name == "platform-admin" }); i >= 0 {
		p := projects.Projects[i]
		roles, err := c.ListProjectRoles(ctx, p.ID, Page{Limit: 100})
		if err != nil || !slices.ContainsFunc(roles, func(r ProjectRole) bool { return r.Key == "viewer" }) {
			t.Fatalf("roles of platform-admin = %+v, %v", roles, err)
		}
		grantID, err := c.CreateAuthorization(ctx, humanID, p.ID, p.OrganizationID, []string{"viewer"})
		if err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool {
			list, err := c.ListAuthorizations(ctx, AuthorizationFilter{UserID: humanID}, Page{Limit: 10})
			return err == nil && list.Total == 1 && list.Authorizations[0].ID == grantID && list.Authorizations[0].Roles[0].Key == "viewer"
		})
		must(t, c.UpdateAuthorization(ctx, grantID, []string{"viewer", "super-admin"}))
		must(t, c.DeleteAuthorization(ctx, grantID))
	} else {
		t.Log("no platform-admin project: role assignments not tested")
	}

	must(t, c.DeleteUser(ctx, machineID))
	must(t, c.DeleteUser(ctx, humanID))
	must(t, c.DeleteOrganization(ctx, orgID))
	if _, err := c.GetUser(ctx, humanID); !HasCode(err, CodeNotFound) {
		t.Errorf("deleted user: %v, want not_found", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Zitadel's projections catch up with a write only eventually.
func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	for range 50 {
		if ok() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("condition not met after 10 s")
}

func randomSuffix(t *testing.T) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
