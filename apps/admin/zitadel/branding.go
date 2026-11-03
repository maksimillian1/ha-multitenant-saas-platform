package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

// Branding is what Zitadel calls a label policy: the colors, flags and
// assets of the login pages. The instance has one, the platform default;
// an organization may have its own. Each has an active version, which the
// login shows, and a preview (the draft), which every change writes to until
// it is activated.
//
// v4.19.2 has no v2 API to change it, so these calls use the v1 REST gateway
// (management API for an organization, admin API for the instance) and the
// assets API (internal/api/assets/generator/asset.yaml).

// Scope is whose branding a call reads or changes. The zero Scope is
// neither, and every call refuses it.
type Scope struct {
	orgID    string
	instance bool
}

// OrgScope is the branding of one organization: management API calls with
// the x-zitadel-orgid header. The token needs policy.read and policy.write
// on the organization (IAM_ORG_MANAGER has them).
func OrgScope(orgID string) Scope { return Scope{orgID: orgID} }

// InstanceScope is the instance's branding, the default of every
// organization. The token needs iam.policy.read and iam.policy.write
// (IAM_OWNER).
func InstanceScope() Scope { return Scope{instance: true} }

// OrgID is the organization of an OrgScope, "" for the instance.
func (s Scope) OrgID() string { return s.orgID }

// IsInstance reports whether the scope is the instance's branding.
func (s Scope) IsInstance() bool { return s.instance }

var errNoScope = errors.New("zitadel: branding call without an organization or the instance")

func (s Scope) check() error {
	if !s.instance && s.orgID == "" {
		return errNoScope
	}
	return nil
}

func (s Scope) policyPath() string {
	if s.instance {
		return "/admin/v1/policies/label"
	}
	return "/management/v1/policies/label"
}

func (s Scope) assetsPath() string {
	if s.instance {
		return "/assets/v1/instance/policy/label/"
	}
	return "/assets/v1/org/policy/label/"
}

// ThemeMode is policy.v1.ThemeMode: whether the login follows the browser
// or is always light or dark.
type ThemeMode string

const (
	ThemeModeAuto  ThemeMode = "THEME_MODE_AUTO"
	ThemeModeLight ThemeMode = "THEME_MODE_LIGHT"
	ThemeModeDark  ThemeMode = "THEME_MODE_DARK"
)

// BrandingSettings are the fields of a label policy that an add or update
// request sets. Colors are hex values such as "#5469d4"; an empty one leaves
// Zitadel's built-in color.
type BrandingSettings struct {
	PrimaryColor        string    `json:"primaryColor"`
	BackgroundColor     string    `json:"backgroundColor"`
	WarnColor           string    `json:"warnColor"`
	FontColor           string    `json:"fontColor"`
	PrimaryColorDark    string    `json:"primaryColorDark"`
	BackgroundColorDark string    `json:"backgroundColorDark"`
	WarnColorDark       string    `json:"warnColorDark"`
	FontColorDark       string    `json:"fontColorDark"`
	HideLoginNameSuffix bool      `json:"hideLoginNameSuffix"`
	DisableWatermark    bool      `json:"disableWatermark"`
	ThemeMode           ThemeMode `json:"themeMode,omitempty"`
}

// Branding is policy.v1.LabelPolicy. IsDefault means an organization has no
// branding of its own and shows the instance's. The asset URLs are object
// names in Zitadel's store, set while the asset exists.
type Branding struct {
	BrandingSettings
	Details     Details `json:"details"`
	IsDefault   bool    `json:"isDefault"`
	LogoURL     string  `json:"logoUrl"`
	IconURL     string  `json:"iconUrl"`
	LogoURLDark string  `json:"logoUrlDark"`
	IconURLDark string  `json:"iconUrlDark"`
	FontURL     string  `json:"fontUrl"`
}

// Asset is one file of a label policy.
type Asset string

const (
	AssetLogo     Asset = "logo"
	AssetLogoDark Asset = "logo-dark"
	AssetIcon     Asset = "icon"
	AssetIconDark Asset = "icon-dark"
	AssetFont     Asset = "font"
)

// Assets lists every asset of a label policy.
var Assets = []Asset{AssetLogo, AssetLogoDark, AssetIcon, AssetIconDark, AssetFont}

// assetRoutes are the asset's path under the assets API and the last path
// segment of its v1 remove call.
var assetRoutes = map[Asset][2]string{
	AssetLogo:     {"logo", "logo"},
	AssetLogoDark: {"logo/dark", "logo_dark"},
	AssetIcon:     {"icon", "icon"},
	AssetIconDark: {"icon/dark", "icon_dark"},
	AssetFont:     {"font", "font"},
}

// HasAsset reports whether the policy has the asset.
func (b Branding) HasAsset(a Asset) bool {
	switch a {
	case AssetLogo:
		return b.LogoURL != ""
	case AssetLogoDark:
		return b.LogoURLDark != ""
	case AssetIcon:
		return b.IconURL != ""
	case AssetIconDark:
		return b.IconURLDark != ""
	case AssetFont:
		return b.FontURL != ""
	}
	return false
}

// MaxAssetSize is the largest logo, icon or font Zitadel accepts
// (1 << 19 in internal/api/assets/login_policy.go).
const MaxAssetSize = 512 << 10

// GetBranding returns the active branding, or the draft with preview. For
// an organization without its own branding it is the instance's, with
// IsDefault set.
func (c *Client) GetBranding(ctx context.Context, s Scope, preview bool) (Branding, error) {
	if err := s.check(); err != nil {
		return Branding{}, err
	}
	path := s.policyPath()
	if preview {
		path += "/_preview"
	}
	var resp struct {
		Policy Branding `json:"policy"`
	}
	if err := c.rest(ctx, http.MethodGet, path, s, nil, &resp); err != nil {
		return Branding{}, err
	}
	return resp.Policy, nil
}

// CreateOrgBranding gives an organization branding of its own, as a draft;
// Zitadel answers already_exists if it has one.
func (c *Client) CreateOrgBranding(ctx context.Context, orgID string, settings BrandingSettings) error {
	s := OrgScope(orgID)
	if err := s.check(); err != nil {
		return err
	}
	return c.rest(ctx, http.MethodPost, s.policyPath(), s, settings, nil)
}

// UpdateBranding changes the draft. An organization must have branding of
// its own (CreateOrgBranding); Zitadel answers not_found otherwise.
func (c *Client) UpdateBranding(ctx context.Context, s Scope, settings BrandingSettings) error {
	if err := s.check(); err != nil {
		return err
	}
	return c.rest(ctx, http.MethodPut, s.policyPath(), s, settings, nil)
}

// ActivateBranding publishes the draft: the login shows it from now on.
func (c *Client) ActivateBranding(ctx context.Context, s Scope) error {
	if err := s.check(); err != nil {
		return err
	}
	return c.rest(ctx, http.MethodPost, s.policyPath()+"/_activate", s, struct{}{}, nil)
}

// ResetOrgBranding removes an organization's own branding, active and
// draft, so that it shows the instance's again.
func (c *Client) ResetOrgBranding(ctx context.Context, orgID string) error {
	s := OrgScope(orgID)
	if err := s.check(); err != nil {
		return err
	}
	return c.rest(ctx, http.MethodDelete, s.policyPath(), s, nil, nil)
}

// RemoveBrandingAsset removes an asset from the draft.
func (c *Client) RemoveBrandingAsset(ctx context.Context, s Scope, a Asset) error {
	route, err := assetRoute(s, a)
	if err != nil {
		return err
	}
	return c.rest(ctx, http.MethodDelete, s.policyPath()+"/"+route[1], s, nil, nil)
}

// UploadBrandingAsset puts an asset into the draft. Zitadel detects the type
// from the content and refuses what is not an image (logo, icon) or a font,
// or is larger than MaxAssetSize.
func (c *Client) UploadBrandingAsset(ctx context.Context, s Scope, a Asset, filename, contentType string, data []byte) error {
	route, err := assetRoute(s, a)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", multipart.FileContentDisposition("file", filename))
	header.Set("Content-Type", contentType)
	part, err := form.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	req, err := c.request(ctx, http.MethodPost, s.assetsPath()+route[0], s, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, answer, err := c.send(req)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return assetError(res.StatusCode, answer)
	}
	return nil
}

// AssetFile is the content of an asset.
type AssetFile struct {
	ContentType string
	Data        []byte
}

// BrandingAsset downloads an asset of the active branding, or of the draft
// with preview; not_found if the policy has none.
func (c *Client) BrandingAsset(ctx context.Context, s Scope, a Asset, preview bool) (AssetFile, error) {
	route, err := assetRoute(s, a)
	if err != nil {
		return AssetFile{}, err
	}
	path := s.assetsPath() + route[0]
	if preview {
		path += "/_preview"
	}
	req, err := c.request(ctx, http.MethodGet, path, s, nil)
	if err != nil {
		return AssetFile{}, err
	}
	res, data, err := c.send(req)
	if err != nil {
		return AssetFile{}, err
	}
	if res.StatusCode != http.StatusOK {
		return AssetFile{}, assetError(res.StatusCode, data)
	}
	return AssetFile{ContentType: res.Header.Get("Content-Type"), Data: data}, nil
}

func assetRoute(s Scope, a Asset) ([2]string, error) {
	if err := s.check(); err != nil {
		return [2]string{}, err
	}
	route, ok := assetRoutes[a]
	if !ok {
		return [2]string{}, errors.New("zitadel: unknown branding asset " + string(a))
	}
	return route, nil
}

// rest sends a v1 REST gateway request with req as its JSON body (none if
// nil) and decodes the answer into resp.
func (c *Client) rest(ctx context.Context, method, path string, s Scope, req, resp any) error {
	var body io.Reader
	if req != nil {
		data, err := json.Marshal(req)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	httpReq, err := c.request(ctx, method, path, s, body)
	if err != nil {
		return err
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	return c.do(httpReq, resp)
}

// request builds a request to path; an organization's calls name it in the
// x-zitadel-orgid header, without which Zitadel acts on the token's own
// organization.
func (c *Client) request(ctx context.Context, method, path string, s Scope, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if !s.instance {
		req.Header.Set("x-zitadel-orgid", s.orgID)
	}
	return req, nil
}

// assetError reads an error of the assets API, which answers in plain text
// (http.Error) rather than in Connect or gateway JSON.
func assetError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(status)
	}
	code := codeFromStatus(status)
	switch status {
	case http.StatusBadRequest:
		code = CodeInvalidArgument
	case http.StatusNotFound:
		code = CodeNotFound
	}
	return &Error{Status: status, Code: code, Message: msg}
}
