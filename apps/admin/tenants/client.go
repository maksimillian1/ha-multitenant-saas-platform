// Package tenants is the admin app's client for Tenant Management
// (ADR-0012, docs/contracts/tenants). It calls with the app's machine token,
// a Zitadel access token for the project tenant-management with the role
// tenant-manager, and names the acting admin on every change.
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

// AdminHeader carries the Zitadel user ID of the admin who asked for a change.
const AdminHeader = "X-Admin-User-Id"

// maxAnswer bounds what is read from an answer: a list of 1 000 tenants is
// far below it.
const maxAnswer = 4 << 20

// Tenant is a tenant as Tenant Management returns it.
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

// Create is the body of POST /tenants.
type Create struct {
	Name       string `json:"name"`
	AdminEmail string `json:"admin_email"`
}

// Error is an answer of Tenant Management that is not a success. Tenant is
// set when the request changed the tenant before it failed.
type Error struct {
	Status  int
	Message string
	Tenant  *Tenant
}

func (e *Error) Error() string {
	return fmt.Sprintf("tenant management: %d: %s", e.Status, e.Message)
}

// Config is where Tenant Management is and how the app gets its token.
type Config struct {
	// URL is Tenant Management's base URL.
	URL string
	// TokenURL is Zitadel's token endpoint.
	TokenURL string
	// ClientID and ClientSecret are the machine user's client credentials.
	ClientID, ClientSecret string
	// Audience is the ID of the Zitadel project tenant-management.
	Audience string
	// Timeout bounds one call; a create or delete runs every step in the
	// request, so it is minutes, not seconds.
	Timeout time.Duration
}

// Scopes asks Zitadel for a token whose audience is the project and that
// holds the project's roles.
func Scopes(audience string) []string {
	return []string{
		"openid",
		"urn:zitadel:iam:org:project:id:" + audience + ":aud",
		"urn:zitadel:iam:org:projects:roles",
	}
}

// Client calls Tenant Management.
type Client struct {
	base string
	http *http.Client
}

// New returns a client. tokenHTTP reaches Zitadel's token endpoint; the
// token is cached until shortly before it expires. ctx bounds the token
// fetches: the app's lifetime, not one request's.
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

// List returns the tenants, newest first; tombstones only when asked.
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

// Get returns one tenant.
func (c *Client) Get(ctx context.Context, id string) (Tenant, error) {
	var t Tenant
	err := c.do(ctx, http.MethodGet, tenantPath(id, ""), "", nil, &t)
	return t, err
}

// Create creates a tenant and runs every provisioning step.
func (c *Client) Create(ctx context.Context, adminID string, in Create) (Tenant, error) {
	var t Tenant
	err := c.do(ctx, http.MethodPost, "/tenants", adminID, in, &t)
	return t, err
}

// Retry resumes a tenant left in provisioning or deleting.
func (c *Client) Retry(ctx context.Context, adminID, id string) (Tenant, error) {
	return c.change(ctx, http.MethodPost, tenantPath(id, "/retry"), adminID)
}

// Suspend suspends an active tenant.
func (c *Client) Suspend(ctx context.Context, adminID, id string) (Tenant, error) {
	return c.change(ctx, http.MethodPost, tenantPath(id, "/suspend"), adminID)
}

// Resume makes a suspended tenant active again.
func (c *Client) Resume(ctx context.Context, adminID, id string) (Tenant, error) {
	return c.change(ctx, http.MethodPost, tenantPath(id, "/resume"), adminID)
}

// Delete deletes a tenant and returns its tombstone.
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

// do sends one request. adminID is required for a change; a read has none.
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
