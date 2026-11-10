package zitadel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// brandingFake answers the v1 REST and assets routes with a canned status,
// content type and body per "METHOD path", and records what it got. The
// paths and bodies are those of Zitadel v4.19.2 (proto/zitadel/management.proto,
// admin.proto, internal/api/assets/generator/asset.yaml).
type brandingFake struct {
	t        *testing.T
	answers  map[string]cannedAnswer
	requests []brandingRequest
}

type cannedAnswer struct {
	status      int
	contentType string
	body        string
}

type brandingRequest struct {
	method, path string
	header       http.Header
	body         []byte
}

func newBrandingFake(t *testing.T, answers map[string]cannedAnswer) (*brandingFake, string, *http.Client) {
	t.Helper()
	f := &brandingFake{t: t, answers: answers}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL, srv.Client()
}

func (f *brandingFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.requests = append(f.requests, brandingRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body})
	answer, ok := f.answers[r.Method+" "+r.URL.Path]
	if !ok {
		answer = cannedAnswer{status: http.StatusOK, contentType: "application/json", body: "{}"}
	}
	w.Header().Set("Content-Type", answer.contentType)
	w.WriteHeader(answer.status)
	_, _ = io.WriteString(w, answer.body)
}

func (f *brandingFake) last() brandingRequest {
	f.t.Helper()
	if len(f.requests) == 0 {
		f.t.Fatal("no request reached Zitadel")
	}
	return f.requests[len(f.requests)-1]
}

var settings = BrandingSettings{
	PrimaryColor: "#1d4ed8", BackgroundColor: "#fafafa", WarnColor: "#cd3d56", FontColor: "#000000",
	PrimaryColorDark: "#2563eb", BackgroundColorDark: "#111827", WarnColorDark: "#ff3b5b", FontColorDark: "#ffffff",
	HideLoginNameSuffix: true, ThemeMode: ThemeModeDark,
}

const settingsJSON = `{"primaryColor": "#1d4ed8", "backgroundColor": "#fafafa", "warnColor": "#cd3d56", "fontColor": "#000000",
	"primaryColorDark": "#2563eb", "backgroundColorDark": "#111827", "warnColorDark": "#ff3b5b", "fontColorDark": "#ffffff",
	"hideLoginNameSuffix": true, "disableWatermark": false, "themeMode": "THEME_MODE_DARK"}`

// TestBrandingCallsCarryTheirToken checks every branding call of an
// organization and of the instance: its method, path, body, the organization
// header and the token of the client that made it (AC-8).
func TestBrandingCallsCarryTheirToken(t *testing.T) {
	ctx := context.Background()
	org, instance := OrgScope("org-a"), InstanceScope()
	for _, tc := range []struct {
		name         string
		call         func(c *Client) error
		method, path string
		orgHeader    string
		body         string // expected JSON body, "" for none
	}{
		{"org active", func(c *Client) error { _, err := c.GetBranding(ctx, org, false); return err },
			"GET", "/management/v1/policies/label", "org-a", ""},
		{"org draft", func(c *Client) error { _, err := c.GetBranding(ctx, org, true); return err },
			"GET", "/management/v1/policies/label/_preview", "org-a", ""},
		{"org create", func(c *Client) error { return c.CreateOrgBranding(ctx, "org-a", settings) },
			"POST", "/management/v1/policies/label", "org-a", settingsJSON},
		{"org update", func(c *Client) error { return c.UpdateBranding(ctx, org, settings) },
			"PUT", "/management/v1/policies/label", "org-a", settingsJSON},
		{"org publish", func(c *Client) error { return c.ActivateBranding(ctx, org) },
			"POST", "/management/v1/policies/label/_activate", "org-a", "{}"},
		{"org reset", func(c *Client) error { return c.ResetOrgBranding(ctx, "org-a") },
			"DELETE", "/management/v1/policies/label", "org-a", ""},
		{"org remove dark logo", func(c *Client) error { return c.RemoveBrandingAsset(ctx, org, AssetLogoDark) },
			"DELETE", "/management/v1/policies/label/logo_dark", "org-a", ""},
		{"org remove font", func(c *Client) error { return c.RemoveBrandingAsset(ctx, org, AssetFont) },
			"DELETE", "/management/v1/policies/label/font", "org-a", ""},
		{"org draft icon", func(c *Client) error { _, err := c.BrandingAsset(ctx, org, AssetIcon, true); return err },
			"GET", "/assets/v1/org/policy/label/icon/_preview", "org-a", ""},
		{"org active dark icon", func(c *Client) error { _, err := c.BrandingAsset(ctx, org, AssetIconDark, false); return err },
			"GET", "/assets/v1/org/policy/label/icon/dark", "org-a", ""},

		{"instance active", func(c *Client) error { _, err := c.GetBranding(ctx, instance, false); return err },
			"GET", "/admin/v1/policies/label", "", ""},
		{"instance draft", func(c *Client) error { _, err := c.GetBranding(ctx, instance, true); return err },
			"GET", "/admin/v1/policies/label/_preview", "", ""},
		{"instance update", func(c *Client) error { return c.UpdateBranding(ctx, instance, settings) },
			"PUT", "/admin/v1/policies/label", "", settingsJSON},
		{"instance publish", func(c *Client) error { return c.ActivateBranding(ctx, instance) },
			"POST", "/admin/v1/policies/label/_activate", "", "{}"},
		{"instance remove icon", func(c *Client) error { return c.RemoveBrandingAsset(ctx, instance, AssetIcon) },
			"DELETE", "/admin/v1/policies/label/icon", "", ""},
		{"instance draft dark logo", func(c *Client) error { _, err := c.BrandingAsset(ctx, instance, AssetLogoDark, true); return err },
			"GET", "/assets/v1/instance/policy/label/logo/dark/_preview", "", ""},
		{"instance active font", func(c *Client) error { _, err := c.BrandingAsset(ctx, instance, AssetFont, false); return err },
			"GET", "/assets/v1/instance/policy/label/font", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, url, httpClient := newBrandingFake(t, nil)
			token := "token-of-admin-app"
			if err := tc.call(NewClient(url, staticTokens(token), httpClient)); err != nil {
				t.Fatal(err)
			}
			got := f.last()
			if got.method != tc.method || got.path != tc.path {
				t.Fatalf("%s %s, want %s %s", got.method, got.path, tc.method, tc.path)
			}
			if auth := got.header.Get("Authorization"); auth != "Bearer "+token {
				t.Errorf("Authorization = %q", auth)
			}
			if h := got.header.Get("X-Zitadel-Orgid"); h != tc.orgHeader {
				t.Errorf("x-zitadel-orgid = %q, want %q", h, tc.orgHeader)
			}
			if tc.body == "" {
				if len(got.body) > 0 {
					t.Errorf("body = %s, want none", got.body)
				}
				return
			}
			var body any
			if err := json.Unmarshal(got.body, &body); err != nil {
				t.Fatalf("body is not JSON: %s", got.body)
			}
			if want := jsonValue(t, tc.body); !reflect.DeepEqual(body, want) {
				t.Errorf("body = %v\nwant   %v", body, want)
			}
		})
	}
}

func TestBrandingNeedsAScope(t *testing.T) {
	f, url, httpClient := newBrandingFake(t, nil)
	c := NewClient(url, staticTokens("t"), httpClient)
	if _, err := c.GetBranding(context.Background(), Scope{}, false); err == nil {
		t.Error("GetBranding without a scope: no error")
	}
	if _, err := c.GetBranding(context.Background(), OrgScope(""), false); err == nil {
		t.Error("GetBranding of an organization without ID: no error")
	}
	if err := c.CreateOrgBranding(context.Background(), "", settings); err == nil {
		t.Error("CreateOrgBranding without ID: no error")
	}
	if len(f.requests) > 0 {
		t.Errorf("%d requests reached Zitadel", len(f.requests))
	}
}

func TestDecodesBranding(t *testing.T) {
	_, url, httpClient := newBrandingFake(t, map[string]cannedAnswer{
		"GET /management/v1/policies/label/_preview": {200, "application/json", `{"policy": {
			"details": {"sequence": "12", "changeDate": "2026-11-03T10:00:00Z", "resourceOwner": "org-a"},
			"primaryColor": "#1d4ed8", "isDefault": true, "hideLoginNameSuffix": true, "warnColor": "#cd3d56",
			"backgroundColor": "#fafafa", "fontColor": "#000000", "primaryColorDark": "#2563eb",
			"backgroundColorDark": "#111827", "warnColorDark": "#ff3b5b", "fontColorDark": "#ffffff",
			"disableWatermark": true, "logoUrl": "org-a/policy/label/logo-1", "iconUrl": "org-a/policy/label/icon-2",
			"logoUrlDark": "org-a/policy/label/logo-dark-3", "fontUrl": "org-a/policy/label/font-4",
			"themeMode": "THEME_MODE_LIGHT"}, "isDefault": true}`},
	})
	got, err := NewClient(url, staticTokens("t"), httpClient).GetBranding(context.Background(), OrgScope("org-a"), true)
	if err != nil {
		t.Fatal(err)
	}
	want := settings
	want.DisableWatermark, want.ThemeMode = true, ThemeModeLight
	if got.BrandingSettings != want {
		t.Errorf("settings = %+v\nwant       %+v", got.BrandingSettings, want)
	}
	if !got.IsDefault || got.Details.ResourceOwner != "org-a" || got.Details.Sequence != 12 {
		t.Errorf("IsDefault %v, details %+v", got.IsDefault, got.Details)
	}
	assets := map[Asset]bool{AssetLogo: true, AssetLogoDark: true, AssetIcon: true, AssetIconDark: false, AssetFont: true}
	for asset, set := range assets {
		if got.HasAsset(asset) != set {
			t.Errorf("HasAsset(%s) = %v, want %v", asset, !set, set)
		}
	}
}

func TestUploadAsset(t *testing.T) {
	for _, tc := range []struct {
		scope Scope
		asset Asset
		path  string
		org   string
	}{
		{OrgScope("org-a"), AssetLogo, "/assets/v1/org/policy/label/logo", "org-a"},
		{OrgScope("org-a"), AssetIconDark, "/assets/v1/org/policy/label/icon/dark", "org-a"},
		{InstanceScope(), AssetFont, "/assets/v1/instance/policy/label/font", ""},
	} {
		t.Run(string(tc.asset), func(t *testing.T) {
			f, url, httpClient := newBrandingFake(t, nil)
			data := []byte("\x89PNG\r\n\x1a\nnot really a png")
			err := NewClient(url, staticTokens("t"), httpClient).UploadBrandingAsset(context.Background(), tc.scope, tc.asset, "logo.png", "image/png", data)
			if err != nil {
				t.Fatal(err)
			}
			got := f.last()
			if got.method != http.MethodPost || got.path != tc.path || got.header.Get("X-Zitadel-Orgid") != tc.org {
				t.Fatalf("%s %s (org %q), want POST %s (org %q)", got.method, got.path, got.header.Get("X-Zitadel-Orgid"), tc.path, tc.org)
			}
			mediaType, params, err := mime.ParseMediaType(got.header.Get("Content-Type"))
			if err != nil || mediaType != "multipart/form-data" {
				t.Fatalf("Content-Type = %q", got.header.Get("Content-Type"))
			}
			part, err := multipart.NewReader(strings.NewReader(string(got.body)), params["boundary"]).NextPart()
			if err != nil {
				t.Fatal(err)
			}
			content, _ := io.ReadAll(part)
			if part.FormName() != "file" || part.FileName() != "logo.png" || part.Header.Get("Content-Type") != "image/png" || string(content) != string(data) {
				t.Errorf("part %q %q %q with %q", part.FormName(), part.FileName(), part.Header.Get("Content-Type"), content)
			}
		})
	}
}

func TestDownloadAsset(t *testing.T) {
	_, url, httpClient := newBrandingFake(t, map[string]cannedAnswer{
		"GET /assets/v1/org/policy/label/logo/_preview": {200, "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"/>`},
		"GET /assets/v1/org/policy/label/logo":          {404, "text/plain; charset=utf-8", "file not found: \n"},
	})
	c := NewClient(url, staticTokens("t"), httpClient)
	file, err := c.BrandingAsset(context.Background(), OrgScope("org-a"), AssetLogo, true)
	if err != nil {
		t.Fatal(err)
	}
	if file.ContentType != "image/svg+xml" || string(file.Data) != `<svg xmlns="http://www.w3.org/2000/svg"/>` {
		t.Errorf("file = %q %q", file.ContentType, file.Data)
	}
	if _, err := c.BrandingAsset(context.Background(), OrgScope("org-a"), AssetLogo, false); !HasCode(err, CodeNotFound) {
		t.Errorf("missing asset: %v, want not_found", err)
	}
}

// TestAssetErrorsCarryAConnectCode: the assets API answers in plain text,
// not in Connect or gateway JSON.
func TestAssetErrorsCarryAConnectCode(t *testing.T) {
	for _, tc := range []struct {
		status    int
		body      string
		code, msg string
	}{
		{400, "file too big, max file size is 512KB\n", CodeInvalidArgument, "file too big, max file size is 512KB"},
		{401, "auth header missing\n", CodeUnauthenticated, "auth header missing"},
		{403, "no permission\n", CodePermissionDenied, "no permission"},
		{404, "file not found\n", CodeNotFound, "file not found"},
		{503, "", CodeUnavailable, "Service Unavailable"},
	} {
		_, url, httpClient := newBrandingFake(t, map[string]cannedAnswer{
			"POST /assets/v1/org/policy/label/logo": {tc.status, "text/plain", tc.body},
		})
		err := NewClient(url, staticTokens("t"), httpClient).UploadBrandingAsset(context.Background(), OrgScope("org-a"), AssetLogo, "a.png", "image/png", []byte("x"))
		var zerr *Error
		if !errors.As(err, &zerr) || zerr.Status != tc.status || zerr.Code != tc.code || zerr.Message != tc.msg {
			t.Errorf("%d: err = %#v, want code %s message %q", tc.status, err, tc.code, tc.msg)
		}
	}
}

func TestBrandingGatewayErrors(t *testing.T) {
	_, url, httpClient := newBrandingFake(t, map[string]cannedAnswer{
		"POST /management/v1/policies/label": {409, "application/json", `{"code": 6, "message": "Private Label Policy has already been created", "details": []}`},
	})
	err := NewClient(url, staticTokens("t"), httpClient).CreateOrgBranding(context.Background(), "org-a", settings)
	if !HasCode(err, CodeAlreadyExists) {
		t.Errorf("err = %v, want already_exists", err)
	}
}
