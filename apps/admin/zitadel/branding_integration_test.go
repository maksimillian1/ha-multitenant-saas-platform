package zitadel

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestBrandingIntegration publishes branding for tenant-a on the kind
// cluster and checks Zitadel's login CSS (HAMSP-37 AC-5, AC-13): tenant-a
// shows the new primary color, while tenant-b's login and the platform
// branding stay as they were. It also uploads, downloads and removes a draft
// logo. Everything it changes is restored. With one account (HAMSP-36) the
// admin app's key also reads and changes the instance branding: a draft is
// saved and put back. It needs the variables of integrationClient.
//
// Verifies: admin-zitadel AC-6
func TestBrandingIntegration(t *testing.T) {
	c, base := integrationClient(t)
	platform := c // the platform branding uses the same account
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tenantA, tenantB := orgByName(ctx, t, c, "tenant-a"), orgByName(ctx, t, c, "tenant-b")
	a := OrgScope(tenantA)

	platformBefore, err := platform.GetBranding(ctx, InstanceScope(), false)
	must(t, err)
	changeInstanceDraft(ctx, t, platform)
	platformCSSBefore := loginCSS(ctx, t, base, tenantB, true)
	tenantBCSSBefore := loginCSS(ctx, t, base, tenantB, false)

	before, err := c.GetBranding(ctx, a, true)
	must(t, err)
	activeBefore, err := c.GetBranding(ctx, a, false)
	must(t, err)
	t.Cleanup(func() { restoreBranding(t, c, tenantA, before, activeBefore) })

	settings := before.BrandingSettings
	settings.PrimaryColor = "#7c3aed"
	settings.ThemeMode = ThemeModeLight
	if before.IsDefault {
		must(t, c.CreateOrgBranding(ctx, tenantA, settings))
	} else {
		must(t, c.UpdateBranding(ctx, a, settings))
	}
	eventually(t, func() bool {
		draft, err := c.GetBranding(ctx, a, true)
		return err == nil && !draft.IsDefault && draft.PrimaryColor == "#7c3aed" && draft.ThemeMode == ThemeModeLight
	})
	if active, err := c.GetBranding(ctx, a, false); err != nil || active.PrimaryColor == "#7c3aed" && activeBefore.PrimaryColor != "#7c3aed" {
		t.Fatalf("saving the draft changed the active branding: %+v, %v", active.BrandingSettings, err)
	}

	// A draft logo goes up and comes back, unless tenant-a has one already.
	if !before.HasAsset(AssetLogo) {
		logo := tinyPNG(t)
		must(t, c.UploadBrandingAsset(ctx, a, AssetLogo, "logo.png", "image/png", logo))
		eventually(t, func() bool {
			file, err := c.BrandingAsset(ctx, a, AssetLogo, true)
			return err == nil && bytes.Equal(file.Data, logo)
		})
		must(t, c.RemoveBrandingAsset(ctx, a, AssetLogo))
		eventually(t, func() bool {
			_, err := c.BrandingAsset(ctx, a, AssetLogo, true)
			return HasCode(err, CodeNotFound)
		})
	}

	must(t, c.ActivateBranding(ctx, a))
	eventually(t, func() bool {
		return strings.Contains(loginCSS(ctx, t, base, tenantA, false), "--zitadel-color-primary-500: rgb(124, 58, 237)")
	})

	// tenant-b and the platform are untouched.
	if got := loginCSS(ctx, t, base, tenantB, false); got != tenantBCSSBefore {
		t.Errorf("tenant-b's login CSS changed:\n%s\nwas\n%s", got, tenantBCSSBefore)
	}
	if got := loginCSS(ctx, t, base, tenantB, true); got != platformCSSBefore {
		t.Error("the platform's login CSS changed")
	}
	if after, err := platform.GetBranding(ctx, InstanceScope(), false); err != nil || after.BrandingSettings != platformBefore.BrandingSettings {
		t.Errorf("the platform branding changed: %+v, %v", after.BrandingSettings, err)
	}
}

// changeInstanceDraft saves a changed draft of the instance branding with the
// admin app's account and puts the old draft back. The active branding stays.
func changeInstanceDraft(ctx context.Context, t *testing.T, c *Client) {
	t.Helper()
	draft, err := c.GetBranding(ctx, InstanceScope(), true)
	must(t, err)
	changed := draft.BrandingSettings
	changed.BackgroundColor = "#fefefe"
	if changed == draft.BrandingSettings {
		changed.BackgroundColor = "#fdfdfd"
	}
	must(t, c.UpdateBranding(ctx, InstanceScope(), changed))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := c.UpdateBranding(ctx, InstanceScope(), draft.BrandingSettings); err != nil {
			t.Errorf("restoring the instance draft: %v", err)
		}
	})
	eventually(t, func() bool {
		got, err := c.GetBranding(ctx, InstanceScope(), true)
		return err == nil && got.BackgroundColor == changed.BackgroundColor
	})
}

// restoreBranding puts tenant-a's branding back: removed if it had none,
// otherwise the old active version published and the old draft saved.
func restoreBranding(t *testing.T, c *Client, orgID string, draft, active Branding) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := OrgScope(orgID)
	if draft.IsDefault {
		if err := c.ResetOrgBranding(ctx, orgID); err != nil {
			t.Errorf("resetting tenant-a's branding: %v", err)
		}
		return
	}
	if !active.IsDefault {
		if err := c.UpdateBranding(ctx, s, active.BrandingSettings); err == nil {
			_ = c.ActivateBranding(ctx, s)
		}
	}
	if err := c.UpdateBranding(ctx, s, draft.BrandingSettings); err != nil && !HasCode(err, CodeInvalidArgument) {
		t.Errorf("restoring tenant-a's draft: %v", err)
	}
}

func orgByName(ctx context.Context, t *testing.T, c *Client, name string) string {
	t.Helper()
	list, err := c.ListOrganizations(ctx, OrganizationFilter{Name: name}, Page{Limit: 10})
	must(t, err)
	for _, org := range list.Organizations {
		if org.Name == name {
			return org.ID
		}
	}
	t.Fatalf("no organization %s; run prototype/bootstrap-tenants.sh", name)
	return ""
}

// loginCSS is the variables.css of the login for an organization: its own
// branding, or the instance's with defaultPolicy.
func loginCSS(ctx context.Context, t *testing.T, base, orgID string, defaultPolicy bool) string {
	t.Helper()
	query := url.Values{
		"orgId":          {orgID},
		"default-policy": {map[bool]string{true: "true", false: "false"}[defaultPolicy]},
		"filename":       {"policy/label/css/variables.css"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/ui/login/resources/dynamic?"+query.Encode(), nil)
	must(t, err)
	res, err := http.DefaultClient.Do(req)
	must(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	must(t, err)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("variables.css for %s: status %d", orgID, res.StatusCode)
	}
	return string(body)
}

// tinyPNG is a valid 2x2 PNG; Zitadel decodes the images it stores.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 124, G: 58, B: 237, A: 255})
	var buf bytes.Buffer
	must(t, png.Encode(&buf, img))
	return buf.Bytes()
}
