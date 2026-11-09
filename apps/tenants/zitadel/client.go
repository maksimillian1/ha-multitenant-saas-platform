// Package zitadel is Tenant Management's client for the Zitadel calls of the
// tenant lifecycle (ADR-0012): the tenant's organization and its first admin.
//
// It calls the v2 services over the Connect protocol with JSON bodies, and
// the v1 management REST gateway for the one call without a v2 equivalent
// (organization members). It needs no SDK and imports nothing from
// github.com/zitadel/zitadel (ADR-0003, C-03). Messages follow the protos of
// Zitadel v4.19.2.
//
// Every method is idempotent: called again after it succeeded, or after an
// attempt whose answer was lost, it ends in the same state and returns nil.
package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
)

const (
	orgService  = "zitadel.org.v2.OrganizationService"
	userService = "zitadel.user.v2.UserService"
)

// Connect error codes the client acts on.
const (
	CodeNotFound           = "not_found"
	CodeAlreadyExists      = "already_exists"
	CodeFailedPrecondition = "failed_precondition"
	CodeDeadlineExceeded   = "deadline_exceeded"
)

// OwnerRole makes a user the administrator of an organization.
const OwnerRole = "ORG_OWNER"

// maxAnswer bounds what the client reads from one answer of Zitadel.
const maxAnswer = 1 << 20

// Client calls Zitadel as Tenant Management's service account.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient returns a client for the Zitadel instance at baseURL (the issuer).
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), token: token, http: httpClient}
}

// Error is an error answer of Zitadel.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("zitadel: %s: %s", e.Code, e.Message) }

// Timeout reports whether Zitadel ran out of time.
func (e *Error) Timeout() bool { return e.Code == CodeDeadlineExceeded }

// HasCode reports whether err is an error answer of Zitadel with the code.
func HasCode(err error, code string) bool {
	var zerr *Error
	return errors.As(err, &zerr) && zerr.Code == code
}

// ErrNameTaken: an organization with the tenant's name exists in Zitadel
// but is not the tenant's.
var ErrNameTaken = fmt.Errorf("zitadel: another organization has this name: %w", core.ErrForeign)

// CreateOrganization creates the tenant's organization with the tenant ID as
// its ID. If it exists already, from an earlier attempt, that is success.
func (c *Client) CreateOrganization(ctx context.Context, id, name string) error {
	req := struct {
		Name           string `json:"name"`
		OrganizationID string `json:"organizationId"`
	}{Name: name, OrganizationID: id}
	err := c.call(ctx, orgService, "AddOrganization", req, nil)
	if !HasCode(err, CodeAlreadyExists) {
		return err
	}
	// The ID or the name is taken: only the ID is ours.
	exists, getErr := c.organizationExists(ctx, id)
	switch {
	case getErr != nil:
		return getErr
	case exists:
		return nil
	default:
		return ErrNameTaken
	}
}

func (c *Client) organizationExists(ctx context.Context, id string) (bool, error) {
	type idQuery struct {
		ID string `json:"id"`
	}
	type query struct {
		IDQuery idQuery `json:"idQuery"`
	}
	req := struct {
		Queries []query `json:"queries"`
	}{Queries: []query{{IDQuery: idQuery{ID: id}}}}
	var resp struct {
		Result []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"result"`
	}
	if err := c.call(ctx, orgService, "ListOrganizations", req, &resp); err != nil {
		return false, err
	}
	for _, o := range resp.Result {
		if o.ID == id && o.State != "ORGANIZATION_STATE_REMOVED" {
			return true, nil
		}
	}
	return false, nil
}

// CreateAdmin creates the tenant's first admin in its organization: a human
// user with the given ID whose username is the email, makes it ORG_OWNER and
// lets Zitadel email an invitation. Already done parts are skipped.
func (c *Client) CreateAdmin(ctx context.Context, orgID, userID, email string) error {
	type organization struct {
		OrgID string `json:"orgId"`
	}
	type profile struct {
		GivenName  string `json:"givenName"`
		FamilyName string `json:"familyName"`
	}
	type emailField struct {
		Email string `json:"email"`
		// Zitadel would send a verification code; the invitation below
		// verifies the email instead, so the code is returned and dropped.
		ReturnCode struct{} `json:"returnCode"`
	}
	user := struct {
		UserID       string       `json:"userId"`
		Username     string       `json:"username"`
		Organization organization `json:"organization"`
		Profile      profile      `json:"profile"`
		Email        emailField   `json:"email"`
	}{
		UserID: userID, Username: email, Organization: organization{OrgID: orgID},
		Profile: profile{GivenName: "Tenant", FamilyName: "Admin"}, Email: emailField{Email: email},
	}
	created := true
	if err := c.call(ctx, userService, "AddHumanUser", user, nil); err != nil {
		if !HasCode(err, CodeAlreadyExists) {
			return err
		}
		created = false
	}

	member := struct {
		UserID string   `json:"userId"`
		Roles  []string `json:"roles"`
	}{UserID: userID, Roles: []string{OwnerRole}}
	if err := c.rest(ctx, http.MethodPost, "/management/v1/orgs/me/members", orgID, member); err != nil &&
		!HasCode(err, CodeAlreadyExists) {
		return err
	}

	invite := struct {
		UserID   string `json:"userId"`
		SendCode struct {
			ApplicationName string `json:"applicationName"`
		} `json:"sendCode"`
	}{UserID: userID}
	invite.SendCode.ApplicationName = "HAMSP"
	err := c.call(ctx, userService, "CreateInviteCode", invite, nil)
	// A user that has already accepted an invitation cannot get another one.
	if err != nil && !created && HasCode(err, CodeFailedPrecondition) {
		return nil
	}
	return err
}

// DeactivateOrganization stops the organization's users from signing in.
func (c *Client) DeactivateOrganization(ctx context.Context, id string) error {
	err := c.call(ctx, orgService, "DeactivateOrganization", organizationRequest{ID: id}, nil)
	if HasCode(err, CodeFailedPrecondition) { // already inactive
		return nil
	}
	return err
}

// ActivateOrganization lets the organization's users sign in again.
func (c *Client) ActivateOrganization(ctx context.Context, id string) error {
	err := c.call(ctx, orgService, "ActivateOrganization", organizationRequest{ID: id}, nil)
	if HasCode(err, CodeFailedPrecondition) { // already active
		return nil
	}
	return err
}

// DeleteOrganization deletes the organization with its users. An
// organization that does not exist is already deleted.
func (c *Client) DeleteOrganization(ctx context.Context, id string) error {
	err := c.call(ctx, orgService, "DeleteOrganization", organizationRequest{ID: id}, nil)
	if HasCode(err, CodeNotFound) {
		return nil
	}
	return err
}

type organizationRequest struct {
	ID string `json:"organizationId"`
}

// call sends one unary Connect request and decodes the answer into resp.
func (c *Client) call(ctx context.Context, service, method string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+service+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Connect-Protocol-Version", "1")
	return c.do(httpReq, resp)
}

// rest sends a v1 REST gateway request on behalf of the organization orgID.
func (c *Client) rest(ctx context.Context, method, path, orgID string, req any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("x-zitadel-orgid", orgID)
	return c.do(httpReq, nil)
}

func (c *Client) do(req *http.Request, resp any) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("zitadel: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("zitadel: reading the answer to %s: %w", req.URL.Path, err)
	}
	if res.StatusCode != http.StatusOK {
		return parseError(res.StatusCode, data)
	}
	if resp == nil {
		return nil
	}
	if err := json.Unmarshal(data, resp); err != nil {
		return fmt.Errorf("zitadel: decoding the answer to %s: %w", req.URL.Path, err)
	}
	return nil
}

// grpcCodes names the numeric gRPC codes of the v1 REST gateway's errors.
var grpcCodes = []string{"ok", "canceled", "unknown", "invalid_argument", CodeDeadlineExceeded, CodeNotFound,
	CodeAlreadyExists, "permission_denied", "resource_exhausted", CodeFailedPrecondition, "aborted",
	"out_of_range", "unimplemented", "internal", "unavailable", "data_loss", "unauthenticated"}

// parseError reads a Connect error ({"code": "not_found", ...}) or a gRPC
// gateway error with a numeric code; without either, the code is "unknown".
func parseError(status int, body []byte) error {
	var answer struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &answer) == nil && len(answer.Code) > 0 {
		var name string
		if json.Unmarshal(answer.Code, &name) == nil && name != "" {
			return &Error{Status: status, Code: name, Message: answer.Message}
		}
		var number int
		if json.Unmarshal(answer.Code, &number) == nil && number > 0 && number < len(grpcCodes) {
			return &Error{Status: status, Code: grpcCodes[number], Message: answer.Message}
		}
	}
	code := "unknown"
	if status == http.StatusGatewayTimeout {
		code = CodeDeadlineExceeded
	}
	return &Error{Status: status, Code: code, Message: http.StatusText(status)}
}
