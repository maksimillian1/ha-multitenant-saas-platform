package api

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"sync"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

// BrandingClient is what the branding routes need from Zitadel for one kind of
// scope. The organization routes use the Zitadel client with the admin app's
// token; the platform routes use PlatformZitadel, a second client with the
// instance token (HAMSP-37), and are the only ones that do.
type BrandingClient interface {
	GetBranding(ctx context.Context, s zitadel.Scope, preview bool) (zitadel.Branding, error)
	UpdateBranding(ctx context.Context, s zitadel.Scope, settings zitadel.BrandingSettings) error
	ActivateBranding(ctx context.Context, s zitadel.Scope) error
	UploadBrandingAsset(ctx context.Context, s zitadel.Scope, a zitadel.Asset, filename, contentType string, data []byte) error
	RemoveBrandingAsset(ctx context.Context, s zitadel.Scope, a zitadel.Asset) error
	BrandingAsset(ctx context.Context, s zitadel.Scope, a zitadel.Asset, preview bool) (zitadel.AssetFile, error)
}

// PlatformZitadel is the client for the instance's branding. Since HAMSP-36
// it is the admin app's own client, whose account holds the custom role
// IAM_BRANDING_MANAGER; the type keeps the platform routes' dependency
// narrow.
type PlatformZitadel interface {
	BrandingClient
}

var _ PlatformZitadel = (*zitadel.Client)(nil)

// brandingTarget is whose branding a request is about and the client that
// may touch it.
type brandingTarget struct {
	scope  zitadel.Scope
	client BrandingClient
}

// targetOf picks the target from the route: /api/orgs/{orgId}/branding is
// the organization, with the admin app's client, after checking that it
// exists; /api/platform/branding is the instance, with the platform client.
type targetOf func(w http.ResponseWriter, r *http.Request) (brandingTarget, bool)

func (s *Service) orgBranding(w http.ResponseWriter, r *http.Request) (brandingTarget, bool) {
	id := r.PathValue("orgId")
	if _, err := s.Zitadel.GetOrganization(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return brandingTarget{}, false
	}
	return brandingTarget{scope: zitadel.OrgScope(id), client: s.Zitadel}, true
}

func (s *Service) platformBranding(http.ResponseWriter, *http.Request) (brandingTarget, bool) {
	return brandingTarget{scope: zitadel.InstanceScope(), client: s.Platform}, true
}

func (s *Service) registerBranding(mux *http.ServeMux) {
	for _, route := range []struct {
		prefix string
		target targetOf
	}{
		{"/api/orgs/{orgId}/branding", s.orgBranding},
		{"/api/platform/branding", s.platformBranding},
	} {
		mux.HandleFunc("GET "+route.prefix, s.read(s.getBranding(route.target)))
		mux.HandleFunc("PUT "+route.prefix, s.change(s.saveBrandingDraft(route.target)))
		mux.HandleFunc("POST "+route.prefix+"/publish", s.change(s.publishBranding(route.target)))
		mux.HandleFunc("GET "+route.prefix+"/assets/{asset}", s.read(s.getBrandingAsset(route.target)))
		mux.HandleFunc("PUT "+route.prefix+"/assets/{asset}", s.change(s.uploadBrandingAsset(route.target)))
		mux.HandleFunc("DELETE "+route.prefix+"/assets/{asset}", s.change(s.removeBrandingAsset(route.target)))
	}
	mux.HandleFunc("DELETE /api/orgs/{orgId}/branding", s.change(s.resetOrgBranding))
}

func (s *Service) getBranding(target targetOf) handler {
	return func(w http.ResponseWriter, r *http.Request, _ core.Session) {
		t, ok := target(w, r)
		if !ok {
			return
		}
		var (
			active, draft       zitadel.Branding
			activeErr, draftErr error
			wg                  sync.WaitGroup
		)
		wg.Go(func() { active, activeErr = t.client.GetBranding(r.Context(), t.scope, false) })
		wg.Go(func() { draft, draftErr = t.client.GetBranding(r.Context(), t.scope, true) })
		wg.Wait()
		if err := cmp.Or(activeErr, draftErr); err != nil {
			s.fail(w, r, err)
			return
		}
		core.WriteJSON(w, http.StatusOK, BrandingState{
			// The draft is the organization's own as soon as it has branding
			// of its own, published or not.
			Inherited: !t.scope.IsInstance() && draft.IsDefault,
			Active:    brandingFromZitadel(active),
			Draft:     brandingFromZitadel(draft),
		})
	}
}

// saveBrandingDraft writes the draft. An organization that shows the
// platform branding first gets branding of its own; the SPA sends the
// platform values it showed, changed or not.
func (s *Service) saveBrandingDraft(target targetOf) handler {
	return func(w http.ResponseWriter, r *http.Request, _ core.Session) {
		var in BrandingSettings
		if !decode(w, r, &in) {
			return
		}
		settings, err := brandingSettingsToZitadel(in)
		if err != nil {
			badRequest(w, err)
			return
		}
		t, ok := target(w, r)
		if !ok {
			return
		}
		createdNow, err := s.ownBranding(r.Context(), t, &settings)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if createdNow {
			noContent(w)
			return
		}
		if err := t.client.UpdateBranding(r.Context(), t.scope, settings); err != nil {
			s.fail(w, r, err)
			return
		}
		noContent(w)
	}
}

// ownBranding gives an organization that shows the platform branding a
// draft of its own: with settings, or with the platform values it showed
// when settings is nil. It reports whether it created one; the instance
// always has its own.
func (s *Service) ownBranding(ctx context.Context, t brandingTarget, settings *zitadel.BrandingSettings) (bool, error) {
	if t.scope.IsInstance() {
		return false, nil
	}
	draft, err := t.client.GetBranding(ctx, t.scope, true)
	if err != nil || !draft.IsDefault {
		return false, err
	}
	if settings == nil {
		settings = &draft.BrandingSettings
	}
	if err := s.Zitadel.CreateOrgBranding(ctx, t.scope.OrgID(), *settings); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) publishBranding(target targetOf) handler {
	return func(w http.ResponseWriter, r *http.Request, _ core.Session) {
		t, ok := target(w, r)
		if !ok {
			return
		}
		if err := t.client.ActivateBranding(r.Context(), t.scope); err != nil {
			s.fail(w, r, err)
			return
		}
		noContent(w)
	}
}

func (s *Service) resetOrgBranding(w http.ResponseWriter, r *http.Request, _ core.Session) {
	t, ok := s.orgBranding(w, r)
	if !ok {
		return
	}
	if err := s.Zitadel.ResetOrgBranding(r.Context(), t.scope.OrgID()); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}

func (s *Service) uploadBrandingAsset(target targetOf) handler {
	return func(w http.ResponseWriter, r *http.Request, _ core.Session) {
		asset, ok := assetParam(w, r)
		if !ok {
			return
		}
		data, status, err := readAssetFile(w, r)
		if err != nil {
			core.WriteError(w, status, err.Error())
			return
		}
		kind := sniff(data)
		if !kind.allowedFor(asset) {
			badRequest(w, fmt.Errorf("the %s must be %s; this file is %s", asset, allowedNames(asset), kind.describe()))
			return
		}
		t, ok := target(w, r)
		if !ok {
			return
		}
		if _, err := s.ownBranding(r.Context(), t, nil); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := t.client.UploadBrandingAsset(r.Context(), t.scope, asset, string(asset)+kind.extension, kind.contentType, data); err != nil {
			s.fail(w, r, err)
			return
		}
		noContent(w)
	}
}

func (s *Service) removeBrandingAsset(target targetOf) handler {
	return func(w http.ResponseWriter, r *http.Request, _ core.Session) {
		asset, ok := assetParam(w, r)
		if !ok {
			return
		}
		t, ok := target(w, r)
		if !ok {
			return
		}
		if err := t.client.RemoveBrandingAsset(r.Context(), t.scope, asset); err != nil {
			s.fail(w, r, err)
			return
		}
		noContent(w)
	}
}

// getBrandingAsset serves an asset from the app's own origin, so the CSP
// (img-src 'self') stays as it is. The type comes from the content, never
// from Zitadel's header, and an SVG cannot run script when opened directly.
func (s *Service) getBrandingAsset(target targetOf) handler {
	return func(w http.ResponseWriter, r *http.Request, _ core.Session) {
		asset, ok := assetParam(w, r)
		if !ok {
			return
		}
		var preview bool
		switch r.URL.Query().Get("version") {
		case "", "active":
		case "draft":
			preview = true
		default:
			badRequest(w, errors.New("version must be active or draft"))
			return
		}
		t, ok := target(w, r)
		if !ok {
			return
		}
		file, err := t.client.BrandingAsset(r.Context(), t.scope, asset, preview)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		kind := sniff(file.Data)
		if !kind.allowedFor(asset) {
			s.Log.WarnContext(r.Context(), "Zitadel served an asset of an unexpected type", "asset", asset, "type", kind.describe())
			core.WriteError(w, http.StatusBadGateway, "the asset in Zitadel is not "+allowedNames(asset))
			return
		}
		w.Header().Set("Content-Type", kind.contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if kind.svg {
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(file.Data)
	}
}

func assetParam(w http.ResponseWriter, r *http.Request) (zitadel.Asset, bool) {
	name := BrandingAssetName(r.PathValue("asset"))
	if !name.Valid() {
		core.WriteError(w, http.StatusNotFound, "no branding asset "+string(name))
		return "", false
	}
	return zitadel.Asset(name), true
}

// readAssetFile reads the part "file" of a multipart body: at most
// zitadel.MaxAssetSize bytes (413 beyond), any other parts skipped.
func readAssetFile(w http.ResponseWriter, r *http.Request) ([]byte, int, error) {
	// Room for the multipart headers around the file.
	r.Body = http.MaxBytesReader(w, r.Body, zitadel.MaxAssetSize+16<<10)
	form, err := r.MultipartReader()
	if err != nil {
		return nil, http.StatusBadRequest, errors.New("the body must be multipart/form-data with a part named file")
	}
	for {
		part, err := form.NextPart()
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, http.StatusRequestEntityTooLarge, errTooLarge
			}
			return nil, http.StatusBadRequest, errors.New("the body has no part named file")
		}
		if part.FormName() != "file" {
			continue
		}
		return readPart(part)
	}
}

var errTooLarge = fmt.Errorf("the file is larger than %d KiB", zitadel.MaxAssetSize>>10)

func readPart(part *multipart.Part) ([]byte, int, error) {
	data, err := io.ReadAll(io.LimitReader(part, zitadel.MaxAssetSize+1))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge) || len(data) > zitadel.MaxAssetSize:
		return nil, http.StatusRequestEntityTooLarge, errTooLarge
	case err != nil:
		return nil, http.StatusBadRequest, errors.New("the file could not be read")
	case len(data) == 0:
		return nil, http.StatusBadRequest, errors.New("the file is empty")
	}
	return data, 0, nil
}

// fileKind is the type of an asset, told by its content.
type fileKind struct {
	contentType string
	extension   string
	image, font bool
	svg         bool
}

var kinds = map[string]fileKind{
	"image/png":  {contentType: "image/png", extension: ".png", image: true},
	"image/jpeg": {contentType: "image/jpeg", extension: ".jpg", image: true},
	"image/gif":  {contentType: "image/gif", extension: ".gif", image: true},
	"image/webp": {contentType: "image/webp", extension: ".webp", image: true},
	"font/ttf":   {contentType: "font/ttf", extension: ".ttf", font: true},
	"font/otf":   {contentType: "font/otf", extension: ".otf", font: true},
	"font/woff":  {contentType: "font/woff", extension: ".woff", font: true},
	"font/woff2": {contentType: "font/woff2", extension: ".woff2", font: true},
}

var svgKind = fileKind{contentType: "image/svg+xml", extension: ".svg", image: true, svg: true}

// svgRoot finds an <svg element near the start of an XML or text file.
var svgRoot = regexp.MustCompile(`(?is)^\s*(<\?xml[^>]*>\s*)?(<!--.*?-->\s*|<!doctype[^>]*>\s*)*<svg[\s>]`)

func sniff(data []byte) fileKind {
	detected := http.DetectContentType(data)
	if kind, ok := kinds[detected]; ok {
		return kind
	}
	if bytes.HasPrefix([]byte(detected), []byte("text/")) && svgRoot.Match(data[:min(len(data), 4096)]) {
		return svgKind
	}
	return fileKind{contentType: detected}
}

func (k fileKind) allowedFor(a zitadel.Asset) bool {
	if a == zitadel.AssetFont {
		return k.font
	}
	return k.image
}

func (k fileKind) describe() string {
	if k.extension != "" {
		return k.contentType
	}
	return "of an unsupported type (" + k.contentType + ")"
}

func allowedNames(a zitadel.Asset) string {
	if a == zitadel.AssetFont {
		return "a TTF, OTF, WOFF or WOFF2 font"
	}
	return "a PNG, JPEG, GIF, WebP or SVG image"
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var themeModes = map[ThemeMode]zitadel.ThemeMode{
	ThemeModeAuto:  zitadel.ThemeModeAuto,
	ThemeModeLight: zitadel.ThemeModeLight,
	ThemeModeDark:  zitadel.ThemeModeDark,
}

func brandingSettingsToZitadel(in BrandingSettings) (zitadel.BrandingSettings, error) {
	for _, c := range []struct{ name, value string }{
		{"primary_color", in.PrimaryColor}, {"background_color", in.BackgroundColor},
		{"warn_color", in.WarnColor}, {"font_color", in.FontColor},
		{"primary_color_dark", in.PrimaryColorDark}, {"background_color_dark", in.BackgroundColorDark},
		{"warn_color_dark", in.WarnColorDark}, {"font_color_dark", in.FontColorDark},
	} {
		if !hexColor.MatchString(c.value) {
			return zitadel.BrandingSettings{}, fmt.Errorf("%s must be a color like #1d4ed8", c.name)
		}
	}
	mode, ok := themeModes[in.ThemeMode]
	if !ok {
		return zitadel.BrandingSettings{}, errors.New("theme_mode must be auto, light or dark")
	}
	return zitadel.BrandingSettings{
		PrimaryColor: in.PrimaryColor, BackgroundColor: in.BackgroundColor,
		WarnColor: in.WarnColor, FontColor: in.FontColor,
		PrimaryColorDark: in.PrimaryColorDark, BackgroundColorDark: in.BackgroundColorDark,
		WarnColorDark: in.WarnColorDark, FontColorDark: in.FontColorDark,
		HideLoginNameSuffix: in.HideLoginNameSuffix, DisableWatermark: in.DisableWatermark,
		ThemeMode: mode,
	}, nil
}

func brandingFromZitadel(b zitadel.Branding) Branding {
	mode := ThemeModeAuto
	for api, z := range themeModes {
		if z == b.ThemeMode {
			mode = api
		}
	}
	out := Branding{
		Settings: BrandingSettings{
			PrimaryColor: b.PrimaryColor, BackgroundColor: b.BackgroundColor,
			WarnColor: b.WarnColor, FontColor: b.FontColor,
			PrimaryColorDark: b.PrimaryColorDark, BackgroundColorDark: b.BackgroundColorDark,
			WarnColorDark: b.WarnColorDark, FontColorDark: b.FontColorDark,
			HideLoginNameSuffix: b.HideLoginNameSuffix, DisableWatermark: b.DisableWatermark,
			ThemeMode: mode,
		},
		Assets: BrandingAssets{
			Logo: b.HasAsset(zitadel.AssetLogo), LogoDark: b.HasAsset(zitadel.AssetLogoDark),
			Icon: b.HasAsset(zitadel.AssetIcon), IconDark: b.HasAsset(zitadel.AssetIconDark),
			Font: b.HasAsset(zitadel.AssetFont),
		},
	}
	if !b.Details.ChangeDate.IsZero() {
		out.ChangedAt = &b.Details.ChangeDate
	}
	return out
}
