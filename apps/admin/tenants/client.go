package tenants

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const AdminHeader = "X-Admin-User-Id"

// A list of 1 000 tenants is far below it.
const maxAnswer = 4 << 20

type Tenant struct {
	ID         string     `json:"id"`
	Name       string     `json:"name,omitempty"`
	AdminEmail string     `json:"admin_email,omitempty"`
	State      string     `json:"state"`
	Parts      []string   `json:"parts"`
	LastError  string     `json:"last_error,omitempty"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedBy  string     `json:"deleted_by,omitempty"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}

type Create struct {
	Name       string `json:"name"`
	AdminEmail string `json:"admin_email"`
}

// Tenant is set when the request changed the tenant before it failed.
type Error struct {
	Status  int
	Message string
	Tenant  *Tenant
}

func (e *Error) Error() string {
	return fmt.Sprintf("tenant management: %d: %s", e.Status, e.Message)
}

type Config struct {
	URL                    string
	TokenURL               string
	ClientID, ClientSecret string
	// ID of the Zitadel project tenant-management.
	Audience string
	// Minutes, not seconds: a create or delete runs every step in the request.
	Timeout time.Duration
}

func Scopes(audience string) []string {
	return []string{
		"openid",
		"urn:zitadel:iam:org:project:id:" + audience + ":aud",
		"urn:zitadel:iam:org:projects:roles",
	}
}

type Client struct {
	base string
	http *http.Client
}

// ctx bounds token fetches for the app's lifetime, not one request's.
func New(ctx context.Context, cfg Config, tokenHTTP *http.Client) *Client {
	cc := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       Scopes(cfg.Audience),
		AuthStyle:    oauth2.AuthStyleInHeader,
	}
	source := cc.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, tokenHTTP))
	return &Client{
		base: strings.TrimRight(cfg.URL, "/"),
		http: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: &oauth2.Transport{Source: source, Base: http.DefaultTransport},
		},
	}
}

func (c *Client) List(ctx context.Context, includeDeleted bool) ([]Tenant, error) {
	path := "/tenants"
	if includeDeleted {
		path += "?include_deleted=true"
	}
	var out struct {
		Tenants []Tenant `json:"tenants"`
	}
	if err := c.do(ctx, http.MethodGet, path, "", nil, &out); err != nil {
		return nil, err
	}
	return out.Tenants, nil
}

func (c *Client) Get(ctx context.Context, id string) (Tenant, error) {
	var t Tenant
	err := c.do(ctx, http.MethodGet, tenantPath(id, ""), "", nil, &t)
	return t, err
}

func (c *Client) Create(ctx context.Context, adminID string, in Create) (Tenant, error) {
	var t Tenant
	err := c.do(ctx, http.MethodPost, "/tenants", adminID, in, &t)
	return t, err
}

func (c *Client) Retry(ctx context.Context, adminID, id string) (Tenant, error) {
	return c.change(ctx, http.MethodPost, tenantPath(id, "/retry"), adminID)
}

func (c *Client) Suspend(ctx context.Context, adminID, id string) (Tenant, error) {
	return c.change(ctx, http.MethodPost, tenantPath(id, "/suspend"), adminID)
}

func (c *Client) Resume(ctx context.Context, adminID, id string) (Tenant, error) {
	return c.change(ctx, http.MethodPost, tenantPath(id, "/resume"), adminID)
}

func (c *Client) Delete(ctx context.Context, adminID, id string) (Tenant, error) {
	return c.change(ctx, http.MethodDelete, tenantPath(id, ""), adminID)
}

func (c *Client) change(ctx context.Context, method, path, adminID string) (Tenant, error) {
	var t Tenant
	err := c.do(ctx, method, path, adminID, nil, &t)
	return t, err
}

func tenantPath(id, suffix string) string {
	return "/tenants/" + url.PathEscape(id) + suffix
}

// adminID is required for a change; a read has none.
func (c *Client) do(ctx context.Context, method, path, adminID string, in, out any) error {
	if method != http.MethodGet && adminID == "" {
		return errors.New("tenant management: a change needs the admin's user ID")
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if adminID != "" {
		req.Header.Set(AdminHeader, adminID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return answerError(resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("tenant management: %s %s: invalid answer: %w", method, path, err)
	}
	return nil
}

func answerError(status int, raw []byte) *Error {
	var body struct {
		Error  string  `json:"error"`
		Tenant *Tenant `json:"tenant"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Error == "" {
		body.Error = http.StatusText(status)
	}
	return &Error{Status: status, Message: body.Error, Tenant: body.Tenant}
}
