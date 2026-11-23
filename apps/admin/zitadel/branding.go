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

// v4.19.2 has no v2 API to change label policies: these calls use the v1 REST gateway and the
// assets API (internal/api/assets/generator/asset.yaml).

// The zero Scope is neither, and every call refuses it.
type Scope struct {
	orgID    string
	instance bool
}

// Needs policy.read and policy.write on the organization (IAM_ORG_MANAGER has them).
func OrgScope(orgID string) Scope { return Scope{orgID: orgID} }

// Needs iam.policy.read and iam.policy.write (IAM_BRANDING_MANAGER).
func InstanceScope() Scope { return Scope{instance: true} }

func (s Scope) OrgID() string { return s.orgID }

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

type ThemeMode string

const (
	ThemeModeAuto  ThemeMode = "THEME_MODE_AUTO"
	ThemeModeLight ThemeMode = "THEME_MODE_LIGHT"
	ThemeModeDark  ThemeMode = "THEME_MODE_DARK"
)

// An empty color leaves Zitadel's built-in one.
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

// IsDefault: an organization without its own branding shows the instance's.
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

type Asset string

const (
	AssetLogo     Asset = "logo"
	AssetLogoDark Asset = "logo-dark"
	AssetIcon     Asset = "icon"
	AssetIconDark Asset = "icon-dark"
	AssetFont     Asset = "font"
)

var Assets = []Asset{AssetLogo, AssetLogoDark, AssetIcon, AssetIconDark, AssetFont}

// Asset path under the assets API, and the last segment of its v1 remove call.
var assetRoutes = map[Asset][2]string{
	AssetLogo:     {"logo", "logo"},
	AssetLogoDark: {"logo/dark", "logo_dark"},
	AssetIcon:     {"icon", "icon"},
	AssetIconDark: {"icon/dark", "icon_dark"},
	AssetFont:     {"font", "font"},
}

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

// 1 << 19 in internal/api/assets/login_policy.go.
const MaxAssetSize = 512 << 10

// Without its own branding an organization gets the instance's, with IsDefault set.
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

// Zitadel answers already_exists if it has one.
func (c *Client) CreateOrgBranding(ctx context.Context, orgID string, settings BrandingSettings) error {
	s := OrgScope(orgID)
	if err := s.check(); err != nil {
		return err
	}
	return c.rest(ctx, http.MethodPost, s.policyPath(), s, settings, nil)
}

// An organization needs its own branding first (CreateOrgBranding), else not_found.
func (c *Client) UpdateBranding(ctx context.Context, s Scope, settings BrandingSettings) error {
	if err := s.check(); err != nil {
		return err
	}
	return c.rest(ctx, http.MethodPut, s.policyPath(), s, settings, nil)
}

func (c *Client) ActivateBranding(ctx context.Context, s Scope) error {
	if err := s.check(); err != nil {
		return err
	}
	return c.rest(ctx, http.MethodPost, s.policyPath()+"/_activate", s, struct{}{}, nil)
}

func (c *Client) ResetOrgBranding(ctx context.Context, orgID string) error {
	s := OrgScope(orgID)
	if err := s.check(); err != nil {
		return err
	}
	return c.rest(ctx, http.MethodDelete, s.policyPath(), s, nil, nil)
}

func (c *Client) RemoveBrandingAsset(ctx context.Context, s Scope, a Asset) error {
	route, err := assetRoute(s, a)
	if err != nil {
		return err
	}
	return c.rest(ctx, http.MethodDelete, s.policyPath()+"/"+route[1], s, nil, nil)
}

// Zitadel refuses what is not an image or font by content, or is larger than MaxAssetSize.
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
	res, answer, err := c.send(req) //nolint:bodyclose // send reads and closes the body before it returns.
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return assetError(res.StatusCode, answer)
	}
	return nil
}

type AssetFile struct {
	ContentType string
	Data        []byte
}

// not_found if the policy has none.
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
	res, data, err := c.send(req) //nolint:bodyclose // send reads and closes the body before it returns.
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

// Without x-zitadel-orgid Zitadel acts on the token's own organization.
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

// The assets API answers in plain text, not Connect or gateway JSON.
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
