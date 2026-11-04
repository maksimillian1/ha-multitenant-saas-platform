package api

import (
	"bytes"
	"context"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

// fakeLabel is one label policy: the draft, and the active version once it
// was activated. Zitadel shows the instance's to an organization whose own
// policy was never activated.
type fakeLabel struct {
	draft, active             *zitadel.Branding
	draftAssets, activeAssets map[zitadel.Asset][]byte
}

var platformColors = zitadel.BrandingSettings{
	PrimaryColor: "#5469d4", BackgroundColor: "#fafafa", WarnColor: "#cd3d56", FontColor: "#000000",
	PrimaryColorDark: "#a5b4fc", BackgroundColorDark: "#111827", WarnColorDark: "#ff3b5b", FontColorDark: "#ffffff",
	ThemeMode: zitadel.ThemeModeAuto,
}

func newFakeLabel(settings zitadel.BrandingSettings) *fakeLabel {
	b := &zitadel.Branding{BrandingSettings: settings, Details: zitadel.Details{ChangeDate: created1}}
	active := *b
	return &fakeLabel{draft: b, active: &active, draftAssets: map[zitadel.Asset][]byte{}, activeAssets: map[zitadel.Asset][]byte{}}
}

// platformLabel is the instance's branding with a logo, active and draft.
func platformLabel() *fakeLabel {
	l := newFakeLabel(platformColors)
	l.draftAssets[zitadel.AssetLogo] = pngFile
	l.activeAssets[zitadel.AssetLogo] = pngFile
	return l
}

// tenantBLabel is tenant-b's own branding: published green, with an SVG
// logo, and a red draft.
func tenantBLabel() *fakeLabel {
	settings := platformColors
	settings.PrimaryColor = "#16a34a"
	l := newFakeLabel(settings)
	l.activeAssets[zitadel.AssetLogo] = svgFile
	l.draft.PrimaryColor = "#dc2626"
	l.draftAssets[zitadel.AssetLogo] = svgFile
	return l
}

// version is the draft, or the active version (nil if never activated).
func (l *fakeLabel) version(preview bool) (*zitadel.Branding, map[zitadel.Asset][]byte) {
	if preview {
		return l.draft, l.draftAssets
	}
	return l.active, l.activeAssets
}

func withAssets(b zitadel.Branding, assets map[zitadel.Asset][]byte) zitadel.Branding {
	url := func(a zitadel.Asset) string {
		if _, ok := assets[a]; ok {
			return "store/" + string(a)
		}
		return ""
	}
	b.LogoURL, b.LogoURLDark = url(zitadel.AssetLogo), url(zitadel.AssetLogoDark)
	b.IconURL, b.IconURLDark = url(zitadel.AssetIcon), url(zitadel.AssetIconDark)
	b.FontURL = url(zitadel.AssetFont)
	return b
}

// fakeUpload is the last asset sent to Zitadel.
type fakeUpload struct {
	scope       zitadel.Scope
	asset       zitadel.Asset
	filename    string
	contentType string
	data        []byte
}

var errInstanceForbidden = &zitadel.Error{Status: http.StatusForbidden, Code: zitadel.CodePermissionDenied,
	Message: "No matching permissions found"}

// label is the policy a call works on. The admin app's account may not touch
// the instance's (IAM_ORG_MANAGER has no iam.policy.write), as in Zitadel.
func (z *fakeZitadel) label(s zitadel.Scope, platform bool) (*fakeLabel, error) {
	switch {
	case s.IsInstance() && !platform:
		return nil, errInstanceForbidden
	case s.IsInstance():
		return z.instanceLabel, nil
	case platform:
		return nil, errInstanceForbidden
	}
	return z.orgLabels[s.OrgID()], nil
}

func (z *fakeZitadel) getBranding(s zitadel.Scope, preview, platform bool) (zitadel.Branding, error) {
	l, err := z.label(s, platform)
	if err != nil {
		return zitadel.Branding{}, err
	}
	if l != nil {
		if b, assets := l.version(preview); b != nil {
			return withAssets(*b, assets), nil
		}
	}
	b := withAssets(*z.instanceLabel.active, z.instanceLabel.activeAssets)
	b.IsDefault = !s.IsInstance()
	return b, nil
}

func (z *fakeZitadel) updateBranding(s zitadel.Scope, settings zitadel.BrandingSettings, platform bool) error {
	l, err := z.label(s, platform)
	if err != nil {
		return err
	}
	if l == nil {
		return notFound("label policy")
	}
	l.draft.BrandingSettings = settings
	l.draft.Details.ChangeDate = created1.Add(time.Hour)
	return nil
}

func (z *fakeZitadel) activateBranding(s zitadel.Scope, platform bool) error {
	l, err := z.label(s, platform)
	if err != nil {
		return err
	}
	if l == nil {
		return notFound("label policy")
	}
	active := *l.draft
	l.active = &active
	l.activeAssets = maps.Clone(l.draftAssets)
	return nil
}

func (z *fakeZitadel) uploadAsset(s zitadel.Scope, a zitadel.Asset, filename, contentType string, data []byte, platform bool) error {
	l, err := z.label(s, platform)
	if err != nil {
		return err
	}
	if l == nil {
		return notFound("label policy")
	}
	l.draftAssets[a] = data
	z.upload = fakeUpload{scope: s, asset: a, filename: filename, contentType: contentType, data: data}
	return nil
}

func (z *fakeZitadel) removeAsset(s zitadel.Scope, a zitadel.Asset, platform bool) error {
	l, err := z.label(s, platform)
	if err != nil {
		return err
	}
	if l == nil {
		return notFound("label policy")
	}
	delete(l.draftAssets, a)
	return nil
}

func (z *fakeZitadel) asset(s zitadel.Scope, a zitadel.Asset, preview, platform bool) (zitadel.AssetFile, error) {
	l, err := z.label(s, platform)
	if err != nil {
		return zitadel.AssetFile{}, err
	}
	assets := z.instanceLabel.activeAssets
	if l != nil {
		if b, own := l.version(preview); b != nil {
			assets = own
		}
	}
	data, ok := assets[a]
	if !ok {
		return zitadel.AssetFile{}, notFound("asset")
	}
	// Zitadel's own header, which the proxy must not pass on.
	return zitadel.AssetFile{ContentType: "text/html", Data: data}, nil
}

func (z *fakeZitadel) GetBranding(_ context.Context, s zitadel.Scope, preview bool) (zitadel.Branding, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("GetBranding"); err != nil {
		return zitadel.Branding{}, err
	}
	return z.getBranding(s, preview, false)
}

func (z *fakeZitadel) CreateOrgBranding(_ context.Context, orgID string, settings zitadel.BrandingSettings) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("CreateOrgBranding"); err != nil {
		return err
	}
	if z.orgLabels[orgID] != nil {
		return &zitadel.Error{Status: http.StatusConflict, Code: zitadel.CodeAlreadyExists, Message: "label policy already exists"}
	}
	l := newFakeLabel(settings)
	l.active = nil
	z.orgLabels[orgID] = l
	return nil
}

func (z *fakeZitadel) UpdateBranding(_ context.Context, s zitadel.Scope, settings zitadel.BrandingSettings) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("UpdateBranding"); err != nil {
		return err
	}
	return z.updateBranding(s, settings, false)
}

func (z *fakeZitadel) ActivateBranding(_ context.Context, s zitadel.Scope) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("ActivateBranding"); err != nil {
		return err
	}
	return z.activateBranding(s, false)
}

func (z *fakeZitadel) ResetOrgBranding(_ context.Context, orgID string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("ResetOrgBranding"); err != nil {
		return err
	}
	if z.orgLabels[orgID] == nil {
		return notFound("label policy")
	}
	delete(z.orgLabels, orgID)
	return nil
}

func (z *fakeZitadel) UploadBrandingAsset(_ context.Context, s zitadel.Scope, a zitadel.Asset, filename, contentType string, data []byte) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("UploadBrandingAsset"); err != nil {
		return err
	}
	return z.uploadAsset(s, a, filename, contentType, data, false)
}

func (z *fakeZitadel) RemoveBrandingAsset(_ context.Context, s zitadel.Scope, a zitadel.Asset) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("RemoveBrandingAsset"); err != nil {
		return err
	}
	return z.removeAsset(s, a, false)
}

func (z *fakeZitadel) BrandingAsset(_ context.Context, s zitadel.Scope, a zitadel.Asset, preview bool) (zitadel.AssetFile, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("BrandingAsset"); err != nil {
		return zitadel.AssetFile{}, err
	}
	return z.asset(s, a, preview, false)
}

// fakePlatform is the client with the instance token. Its calls are recorded
// in the same list, as "platform.<method>", and fail with the same err; it
// may touch only the instance's branding.
type fakePlatform struct{ z *fakeZitadel }

func (p fakePlatform) GetBranding(_ context.Context, s zitadel.Scope, preview bool) (zitadel.Branding, error) {
	p.z.mu.Lock()
	defer p.z.mu.Unlock()
	if err := p.z.record("platform.GetBranding"); err != nil {
		return zitadel.Branding{}, err
	}
	return p.z.getBranding(s, preview, true)
}

func (p fakePlatform) UpdateBranding(_ context.Context, s zitadel.Scope, settings zitadel.BrandingSettings) error {
	p.z.mu.Lock()
	defer p.z.mu.Unlock()
	if err := p.z.record("platform.UpdateBranding"); err != nil {
		return err
	}
	return p.z.updateBranding(s, settings, true)
}

func (p fakePlatform) ActivateBranding(_ context.Context, s zitadel.Scope) error {
	p.z.mu.Lock()
	defer p.z.mu.Unlock()
	if err := p.z.record("platform.ActivateBranding"); err != nil {
		return err
	}
	return p.z.activateBranding(s, true)
}

func (p fakePlatform) UploadBrandingAsset(_ context.Context, s zitadel.Scope, a zitadel.Asset, filename, contentType string, data []byte) error {
	p.z.mu.Lock()
	defer p.z.mu.Unlock()
	if err := p.z.record("platform.UploadBrandingAsset"); err != nil {
		return err
	}
	return p.z.uploadAsset(s, a, filename, contentType, data, true)
}

func (p fakePlatform) RemoveBrandingAsset(_ context.Context, s zitadel.Scope, a zitadel.Asset) error {
	p.z.mu.Lock()
	defer p.z.mu.Unlock()
	if err := p.z.record("platform.RemoveBrandingAsset"); err != nil {
		return err
	}
	return p.z.removeAsset(s, a, true)
}

func (p fakePlatform) BrandingAsset(_ context.Context, s zitadel.Scope, a zitadel.Asset, preview bool) (zitadel.AssetFile, error) {
	p.z.mu.Lock()
	defer p.z.mu.Unlock()
	if err := p.z.record("platform.BrandingAsset"); err != nil {
		return zitadel.AssetFile{}, err
	}
	return p.z.asset(s, a, preview, true)
}

// multipartFile is a request body with one file part, as the SPA sends an
// asset.
type multipartFile struct {
	field, filename, contentType string
	data                         []byte
}

func fileBody(data []byte) multipartFile {
	return multipartFile{field: "file", filename: "upload.bin", contentType: "application/octet-stream", data: data}
}

func (f multipartFile) encode(t *testing.T) (io.Reader, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", multipart.FileContentDisposition(f.field, f.filename))
	header.Set("Content-Type", f.contentType)
	part, err := form.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(f.data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, form.FormDataContentType()
}

// Small valid files of each kind.
var (
	pngFile  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	svgFile  = []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"></svg>`)
	woffFile = append([]byte("wOF2"), make([]byte, 32)...)
	htmlFile = []byte(`<html><script>alert(1)</script></html>`)
)
