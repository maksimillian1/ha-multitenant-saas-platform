package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

// brandingInput is a valid draft: the platform colors with another primary.
func brandingInput() BrandingSettings {
	return BrandingSettings{
		PrimaryColor: "#7c3aed", BackgroundColor: "#fafafa", WarnColor: "#cd3d56", FontColor: "#000000",
		PrimaryColorDark: "#c4b5fd", BackgroundColorDark: "#111827", WarnColorDark: "#ff3b5b", FontColorDark: "#ffffff",
		HideLoginNameSuffix: true, ThemeMode: ThemeModeDark,
	}
}

func getBranding(t *testing.T, e *env, target string) BrandingState {
	t.Helper()
	var state BrandingState
	expect(t, e.do("GET", target, nil), http.StatusOK, &state)
	return state
}

// changed reports whether any of the methods reached Zitadel.
func changed(e *env, methods ...string) []string {
	var hit []string
	for _, call := range e.z.called() {
		if slices.Contains(methods, call) {
			hit = append(hit, call)
		}
	}
	return hit
}

var brandingWrites = []string{"CreateOrgBranding", "UpdateBranding", "ActivateBranding", "ResetOrgBranding",
	"UploadBrandingAsset", "RemoveBrandingAsset", "platform.UpdateBranding", "platform.ActivateBranding",
	"platform.UploadBrandingAsset", "platform.RemoveBrandingAsset"}

func TestGetOrgBranding(t *testing.T) {
	e := newEnv(t)

	inherited := getBranding(t, e, "/api/orgs/org-a/branding")
	if !inherited.Inherited || inherited.Active.Settings.PrimaryColor != "#5469d4" || inherited.Draft.Settings.PrimaryColor != "#5469d4" {
		t.Errorf("tenant-a: %+v, want the platform branding, inherited", inherited)
	}
	if !inherited.Active.Assets.Logo || inherited.Active.Assets.Icon {
		t.Errorf("tenant-a assets: %+v, want the platform logo only", inherited.Active.Assets)
	}

	own := getBranding(t, e, "/api/orgs/org-b/branding")
	if own.Inherited {
		t.Error("tenant-b has its own branding but is shown as inherited")
	}
	if own.Active.Settings.PrimaryColor != "#16a34a" || own.Draft.Settings.PrimaryColor != "#dc2626" {
		t.Errorf("tenant-b: active %s, draft %s; want #16a34a and #dc2626",
			own.Active.Settings.PrimaryColor, own.Draft.Settings.PrimaryColor)
	}
	if own.Draft.Settings.ThemeMode != ThemeModeAuto || own.Draft.ChangedAt == nil {
		t.Errorf("tenant-b draft: theme %q, changed_at %v", own.Draft.Settings.ThemeMode, own.Draft.ChangedAt)
	}

	expect(t, e.do("GET", "/api/orgs/nope/branding", nil), http.StatusNotFound, nil)
	if calls := changed(e, "GetBranding"); len(calls) != 4 {
		t.Errorf("GetBranding called %d times, want 4 (none for an unknown organization)", len(calls))
	}
}

func TestSaveDraftChangesOnlyThePreview(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("PUT", "/api/orgs/org-b/branding", brandingInput()), http.StatusNoContent, nil)

	state := getBranding(t, e, "/api/orgs/org-b/branding")
	if state.Draft.Settings != brandingInput() {
		t.Errorf("draft %+v, want %+v", state.Draft.Settings, brandingInput())
	}
	if state.Active.Settings.PrimaryColor != "#16a34a" {
		t.Errorf("active primary %s, want #16a34a unchanged", state.Active.Settings.PrimaryColor)
	}
	if calls := changed(e, brandingWrites...); !slices.Equal(calls, []string{"UpdateBranding"}) {
		t.Errorf("writes %v, want only UpdateBranding", calls)
	}
}

func TestFirstSaveCreatesCustomPolicy(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("PUT", "/api/orgs/org-a/branding", brandingInput()), http.StatusNoContent, nil)

	state := getBranding(t, e, "/api/orgs/org-a/branding")
	if state.Inherited || state.Draft.Settings != brandingInput() {
		t.Errorf("tenant-a after the first save: %+v", state)
	}
	if state.Active.Settings.PrimaryColor != "#5469d4" {
		t.Errorf("active primary %s, want the platform's until published", state.Active.Settings.PrimaryColor)
	}
	if calls := changed(e, brandingWrites...); !slices.Equal(calls, []string{"CreateOrgBranding"}) {
		t.Errorf("writes %v, want only CreateOrgBranding", calls)
	}
	if platform := getBranding(t, e, "/api/platform/branding"); platform.Draft.Settings.PrimaryColor != "#5469d4" {
		t.Errorf("the platform draft changed to %s", platform.Draft.Settings.PrimaryColor)
	}
}

func TestBadColorIsRefused(t *testing.T) {
	for name, edit := range map[string]func(*BrandingSettings){
		"short":         func(b *BrandingSettings) { b.PrimaryColor = "#fff" },
		"name":          func(b *BrandingSettings) { b.FontColor = "red" },
		"not hex":       func(b *BrandingSettings) { b.WarnColorDark = "#gggggg" },
		"css injection": func(b *BrandingSettings) { b.BackgroundColor = "#000000;}body{" },
		"empty":         func(b *BrandingSettings) { b.PrimaryColorDark = "" },
		"theme mode":    func(b *BrandingSettings) { b.ThemeMode = "sepia" },
	} {
		t.Run(name, func(t *testing.T) {
			for _, target := range []string{"/api/orgs/org-b/branding", "/api/platform/branding"} {
				e := newEnv(t)
				in := brandingInput()
				edit(&in)
				rec := e.do("PUT", target, in)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("PUT %s: status %d, want 400", target, rec.Code)
				}
				errorText(t, rec)
				if calls := e.z.called(); len(calls) > 0 {
					t.Errorf("PUT %s called Zitadel: %v", target, calls)
				}
			}
		})
	}

	// A missing field and an unknown one are refused too.
	e := newEnv(t)
	expect(t, e.do("PUT", "/api/orgs/org-b/branding", `{"primary_color":"#000000"}`), http.StatusBadRequest, nil)
	expect(t, e.do("PUT", "/api/orgs/org-b/branding", `{"logo_url":"http://evil.test/x.png"}`), http.StatusBadRequest, nil)
	if calls := changed(e, brandingWrites...); len(calls) > 0 {
		t.Errorf("writes %v, want none", calls)
	}
}

func TestAssetUpload(t *testing.T) {
	for _, tc := range []struct {
		asset    zitadel.Asset
		file     []byte
		filename string
		mime     string
	}{
		{zitadel.AssetLogo, pngFile, "logo.png", "image/png"},
		{zitadel.AssetLogoDark, svgFile, "logo-dark.svg", "image/svg+xml"},
		{zitadel.AssetIcon, []byte("GIF89a" + strings.Repeat("\x00", 16)), "icon.gif", "image/gif"},
		{zitadel.AssetIconDark, append([]byte("\xff\xd8\xff\xe0"), make([]byte, 16)...), "icon-dark.jpg", "image/jpeg"},
		{zitadel.AssetFont, woffFile, "font.woff2", "font/woff2"},
	} {
		e := newEnv(t)
		// The browser's file name and type are ignored: the content decides.
		body := multipartFile{field: "file", filename: "../../evil.html", contentType: "text/html", data: tc.file}
		expect(t, e.do("PUT", "/api/orgs/org-b/branding/assets/"+string(tc.asset), body), http.StatusNoContent, nil)
		up := e.z.upload
		if up.scope != zitadel.OrgScope("org-b") || up.asset != tc.asset || up.filename != tc.filename ||
			up.contentType != tc.mime || !bytes.Equal(up.data, tc.file) {
			t.Errorf("%s: uploaded %+v, want %s as %s", tc.asset, up, tc.filename, tc.mime)
		}
	}

	// An organization that inherits gets its own draft first, from the
	// platform values.
	e := newEnv(t)
	expect(t, e.do("PUT", "/api/orgs/org-a/branding/assets/logo", fileBody(pngFile)), http.StatusNoContent, nil)
	if calls := changed(e, brandingWrites...); !slices.Equal(calls, []string{"CreateOrgBranding", "UploadBrandingAsset"}) {
		t.Errorf("writes %v, want CreateOrgBranding then UploadBrandingAsset", calls)
	}
	state := getBranding(t, e, "/api/orgs/org-a/branding")
	if state.Inherited || !state.Draft.Assets.Logo || state.Draft.Settings.PrimaryColor != "#5469d4" {
		t.Errorf("tenant-a after the upload: %+v", state)
	}

	expect(t, e.do("DELETE", "/api/orgs/org-a/branding/assets/logo", nil), http.StatusNoContent, nil)
	if state := getBranding(t, e, "/api/orgs/org-a/branding"); state.Draft.Assets.Logo {
		t.Error("the logo is still in the draft after DELETE")
	}
}

func TestAssetTypeAndSizeAreChecked(t *testing.T) {
	big := append(slices.Clone(pngFile), make([]byte, zitadel.MaxAssetSize)...)
	for _, tc := range []struct {
		name   string
		target string
		body   any
		status int
	}{
		{"HTML as a logo", "/api/orgs/org-b/branding/assets/logo", fileBody(htmlFile), http.StatusBadRequest},
		{"a font as a logo", "/api/orgs/org-b/branding/assets/logo", fileBody(woffFile), http.StatusBadRequest},
		{"an image as the font", "/api/platform/branding/assets/font", fileBody(pngFile), http.StatusBadRequest},
		{"plain text as the icon", "/api/orgs/org-b/branding/assets/icon", fileBody([]byte("hello")), http.StatusBadRequest},
		{"an empty file", "/api/orgs/org-b/branding/assets/icon", fileBody(nil), http.StatusBadRequest},
		{"over 512 KiB", "/api/orgs/org-b/branding/assets/logo", fileBody(big), http.StatusRequestEntityTooLarge},
		{"over 512 KiB, platform", "/api/platform/branding/assets/logo", fileBody(big), http.StatusRequestEntityTooLarge},
		{"no file part", "/api/orgs/org-b/branding/assets/logo",
			multipartFile{field: "other", filename: "x.png", contentType: "image/png", data: pngFile}, http.StatusBadRequest},
		{"JSON instead of multipart", "/api/orgs/org-b/branding/assets/logo", `{"file":"x"}`, http.StatusBadRequest},
		{"unknown asset", "/api/orgs/org-b/branding/assets/favicon", fileBody(pngFile), http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			rec := e.do("PUT", tc.target, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d; body %s", rec.Code, tc.status, rec.Body)
			}
			errorText(t, rec)
			if calls := e.z.called(); len(calls) > 0 {
				t.Errorf("called Zitadel: %v", calls)
			}
		})
	}

	// Exactly 512 KiB is accepted.
	limit := append(slices.Clone(pngFile), make([]byte, zitadel.MaxAssetSize-len(pngFile))...)
	expect(t, newEnv(t).do("PUT", "/api/orgs/org-b/branding/assets/logo", fileBody(limit)), http.StatusNoContent, nil)
}

func TestPublish(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("PUT", "/api/orgs/org-b/branding", brandingInput()), http.StatusNoContent, nil)
	expect(t, e.do("POST", "/api/orgs/org-b/branding/publish", nil), http.StatusNoContent, nil)

	state := getBranding(t, e, "/api/orgs/org-b/branding")
	if state.Active.Settings != brandingInput() {
		t.Errorf("active %+v, want the published draft", state.Active.Settings)
	}
	if other := getBranding(t, e, "/api/orgs/org-a/branding"); other.Active.Settings.PrimaryColor != "#5469d4" {
		t.Errorf("tenant-a's active primary changed to %s", other.Active.Settings.PrimaryColor)
	}
	if platform := getBranding(t, e, "/api/platform/branding"); platform.Active.Settings.PrimaryColor != "#5469d4" {
		t.Errorf("the platform's active primary changed to %s", platform.Active.Settings.PrimaryColor)
	}

	// An organization without its own branding has nothing to publish.
	expect(t, e.do("POST", "/api/orgs/org-a/branding/publish", nil), http.StatusNotFound, nil)
}

func TestResetOrgBranding(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("DELETE", "/api/orgs/org-b/branding", nil), http.StatusNoContent, nil)
	state := getBranding(t, e, "/api/orgs/org-b/branding")
	if !state.Inherited || state.Active.Settings.PrimaryColor != "#5469d4" {
		t.Errorf("tenant-b after the reset: %+v, want the platform branding", state)
	}
	expect(t, e.do("DELETE", "/api/orgs/org-b/branding", nil), http.StatusNotFound, nil)
	expect(t, e.do("DELETE", "/api/orgs/nope/branding", nil), http.StatusNotFound, nil)
	if calls := changed(e, "ResetOrgBranding"); len(calls) != 2 {
		t.Errorf("ResetOrgBranding called %d times, want 2 (not for an unknown organization)", len(calls))
	}
}

func TestAssetProxyHeaders(t *testing.T) {
	e := newEnv(t)

	png := e.do("GET", "/api/platform/branding/assets/logo", nil)
	expect(t, png, http.StatusOK, nil)
	if got := png.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type %q, want image/png (not Zitadel's header)", got)
	}
	if png.Header().Get("X-Content-Type-Options") != "nosniff" || png.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("PNG headers %v", png.Header())
	}
	if !bytes.Equal(png.Body.Bytes(), pngFile) {
		t.Error("the PNG body differs from the asset")
	}

	svg := e.do("GET", "/api/orgs/org-b/branding/assets/logo?version=draft", nil)
	expect(t, svg, http.StatusOK, nil)
	if svg.Header().Get("Content-Type") != "image/svg+xml" || svg.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(svg.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("SVG headers %v", svg.Header())
	}
	if svg.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", svg.Header().Get("Cache-Control"))
	}

	// tenant-a inherits, so it shows the platform logo.
	expect(t, e.do("GET", "/api/orgs/org-a/branding/assets/logo", nil), http.StatusOK, nil)
	expect(t, e.do("GET", "/api/orgs/org-b/branding/assets/icon", nil), http.StatusNotFound, nil)
	expect(t, e.do("GET", "/api/orgs/org-b/branding/assets/logo?version=next", nil), http.StatusBadRequest, nil)

	// Something Zitadel holds that is not an image is not passed on.
	e.z.mu.Lock()
	e.z.orgLabels["org-b"].activeAssets[zitadel.AssetLogo] = htmlFile
	e.z.mu.Unlock()
	rec := e.do("GET", "/api/orgs/org-b/branding/assets/logo", nil)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "script") {
		t.Errorf("HTML asset: status %d, body %s; want 502 without the content", rec.Code, rec.Body)
	}
}

func TestPlatformBranding(t *testing.T) {
	e := newEnv(t)
	state := getBranding(t, e, "/api/platform/branding")
	if state.Inherited || state.Active.Settings.PrimaryColor != "#5469d4" || !state.Draft.Assets.Logo {
		t.Errorf("platform branding %+v", state)
	}

	expect(t, e.do("PUT", "/api/platform/branding", brandingInput()), http.StatusNoContent, nil)
	expect(t, e.do("PUT", "/api/platform/branding/assets/icon", fileBody(svgFile)), http.StatusNoContent, nil)
	if state := getBranding(t, e, "/api/platform/branding"); state.Active.Settings.PrimaryColor != "#5469d4" ||
		state.Draft.Settings != brandingInput() || !state.Draft.Assets.Icon || state.Active.Assets.Icon {
		t.Errorf("platform before publishing: %+v", state)
	}
	expect(t, e.do("POST", "/api/platform/branding/publish", nil), http.StatusNoContent, nil)
	if state := getBranding(t, e, "/api/platform/branding"); state.Active.Settings != brandingInput() || !state.Active.Assets.Icon {
		t.Errorf("platform after publishing: %+v", state)
	}
	expect(t, e.do("DELETE", "/api/platform/branding/assets/icon", nil), http.StatusNoContent, nil)

	// tenant-a inherits the published platform branding; tenant-b keeps its own.
	if got := getBranding(t, e, "/api/orgs/org-a/branding").Active.Settings.PrimaryColor; got != "#7c3aed" {
		t.Errorf("tenant-a shows %s, want the new platform primary", got)
	}
	if got := getBranding(t, e, "/api/orgs/org-b/branding").Active.Settings.PrimaryColor; got != "#16a34a" {
		t.Errorf("tenant-b shows %s, want its own", got)
	}
	expect(t, e.do("DELETE", "/api/platform/branding", nil), http.StatusMethodNotAllowed, nil)

	// Instance calls go only through the platform client, and organization
	// calls never do.
	if calls := changed(e, "UpdateBranding", "ActivateBranding", "UploadBrandingAsset", "RemoveBrandingAsset"); len(calls) > 0 {
		t.Errorf("the platform routes called %v with the admin app's client", calls)
	}
	e.z.mu.Lock()
	upload := e.z.upload
	e.z.mu.Unlock()
	if !upload.scope.IsInstance() {
		t.Errorf("the icon went to %+v, want the instance", upload.scope)
	}
}

// TestOrgClientCannotReachTheInstance checks the fake does what Zitadel does,
// so the tests above prove which client each route uses.
func TestOrgClientCannotReachTheInstance(t *testing.T) {
	z := newFakeZitadel()
	if _, err := z.GetBranding(context.Background(), zitadel.InstanceScope(), false); !zitadel.HasCode(err, zitadel.CodePermissionDenied) {
		t.Errorf("org client on the instance: %v, want permission_denied", err)
	}
	if _, err := (fakePlatform{z}).GetBranding(context.Background(), zitadel.OrgScope("org-a"), false); !zitadel.HasCode(err, zitadel.CodePermissionDenied) {
		t.Errorf("platform client on an organization: %v, want permission_denied", err)
	}
}

func TestBrandingZitadelErrorsAreMapped(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{
		{zitadel.CodeNotFound, http.StatusNotFound},
		{zitadel.CodeInvalidArgument, http.StatusBadRequest},
		{zitadel.CodeAlreadyExists, http.StatusConflict},
		{zitadel.CodePermissionDenied, http.StatusBadGateway},
		{zitadel.CodeDeadlineExceeded, http.StatusGatewayTimeout},
		{zitadel.CodeUnavailable, http.StatusBadGateway},
		{"internal", http.StatusBadGateway},
	} {
		for _, req := range []struct {
			method, target string
			body           any
		}{
			{"GET", "/api/platform/branding", nil},
			{"PUT", "/api/platform/branding", brandingInput()},
			{"POST", "/api/platform/branding/publish", nil},
			{"PUT", "/api/platform/branding/assets/logo", fileBody(pngFile)},
			{"GET", "/api/platform/branding/assets/logo", nil},
			{"DELETE", "/api/platform/branding/assets/logo", nil},
		} {
			e := newEnv(t)
			e.z.err = &zitadel.Error{Status: 500, Code: tc.code, Message: "secret internals: sql: no rows"}
			rec := e.do(req.method, req.target, req.body)
			if rec.Code != tc.status {
				t.Errorf("%s: %s %s: status %d, want %d", tc.code, req.method, req.target, rec.Code, tc.status)
			}
			msg := errorText(t, rec)
			leaks := tc.status >= 500 && strings.Contains(msg, "secret internals")
			if leaks {
				t.Errorf("%s: %s %s leaks Zitadel internals: %q", tc.code, req.method, req.target, msg)
			}
		}
	}
}

func TestBrandingChangesAreLogged(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("PUT", "/api/platform/branding", brandingInput()), http.StatusNoContent, nil)
	expect(t, e.do("POST", "/api/orgs/org-b/branding/publish", nil), http.StatusNoContent, nil)
	expect(t, e.do("PUT", "/api/orgs/org-b/branding/assets/logo", fileBody(htmlFile)), http.StatusBadRequest, nil)

	type entry struct {
		Msg, Actor, Action, Target string
		Status                     int
	}
	var changes []entry
	for line := range strings.SplitSeq(strings.TrimSpace(e.logs.String()), "\n") {
		var got entry
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if got.Msg == "admin change" {
			changes = append(changes, got)
		}
	}
	want := []entry{
		{"admin change", "admin", "PUT /api/platform/branding", "instance", http.StatusNoContent},
		{"admin change", "admin", "POST /api/orgs/{orgId}/branding/publish", "org-b", http.StatusNoContent},
		{"admin change", "admin", "PUT /api/orgs/{orgId}/branding/assets/{asset}", "org-b", http.StatusBadRequest},
	}
	if !slices.Equal(changes, want) {
		t.Fatalf("change log\n%+v\nwant\n%+v", changes, want)
	}
}
