// Package zitadel imports nothing from github.com/zitadel/zitadel (ADR-0003, C-03); messages follow
// the protos of Zitadel v4.19.2. Every method is idempotent, also after a lost answer.
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

const (
	CodeNotFound           = "not_found"
	CodeAlreadyExists      = "already_exists"
	CodeFailedPrecondition = "failed_precondition"
	CodeDeadlineExceeded   = "deadline_exceeded"
	CodeUnauthenticated    = "unauthenticated"
)

const OwnerRole = "ORG_OWNER"

const maxAnswer = 1 << 20

type Client struct {
	baseURL string
	tokens  Tokens
	http    *http.Client
}

func NewClient(baseURL string, tokens Tokens, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), tokens: tokens, http: httpClient}
}

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("zitadel: %s: %s", e.Code, e.Message) }

func (e *Error) Timeout() bool { return e.Code == CodeDeadlineExceeded }

func HasCode(err error, code string) bool {
	var zerr *Error
	return errors.As(err, &zerr) && zerr.Code == code
}

var ErrNameTaken = fmt.Errorf("zitadel: another organization has this name: %w", core.ErrForeign)

var ErrAdminEmailTaken = fmt.Errorf("zitadel: the admin email is another user's login name: %w", core.ErrForeign)

// An organization existing from an earlier attempt is success.
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

// Already done parts are skipped.
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
		// The invitation below verifies the email, so the verification code is dropped.
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
		// Login names are unique in the instance, so already_exists may be another user's.
		if err := c.checkOwnUser(ctx, orgID, userID); err != nil {
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

func (c *Client) checkOwnUser(ctx context.Context, orgID, userID string) error {
	var resp struct {
		User struct {
			Details struct {
				ResourceOwner string `json:"resourceOwner"`
			} `json:"details"`
		} `json:"user"`
	}
	err := c.call(ctx, userService, "GetUserByID", struct {
		UserID string `json:"userId"`
	}{userID}, &resp)
	if HasCode(err, CodeNotFound) || (err == nil && resp.User.Details.ResourceOwner != orgID) {
		return ErrAdminEmailTaken
	}
	return err
}

func (c *Client) DeactivateOrganization(ctx context.Context, id string) error {
	err := c.call(ctx, orgService, "DeactivateOrganization", organizationRequest{ID: id}, nil)
	if HasCode(err, CodeFailedPrecondition) {
		return nil
	}
	return err
}

func (c *Client) ActivateOrganization(ctx context.Context, id string) error {
	err := c.call(ctx, orgService, "ActivateOrganization", organizationRequest{ID: id}, nil)
	if HasCode(err, CodeFailedPrecondition) {
		return nil
	}
	return err
}

// An organization that does not exist is already deleted.
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

// v1 REST gateway: organization members have no v2 call.
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
	res, data, err := c.send(req) //nolint:bodyclose // send reads and closes the body before it returns.
	if err != nil {
		return err
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

// On 401 (revoked, or expired early) the token is dropped and req is sent once more.
func (c *Client) send(req *http.Request) (*http.Response, []byte, error) {
	res, data, token, err := c.sendWithToken(req)
	if err != nil || res.StatusCode != http.StatusUnauthorized {
		return res, data, err
	}
	c.tokens.Invalidate(token)
	retry := req.Clone(req.Context())
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return res, data, nil
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, nil, fmt.Errorf("zitadel: repeating %s: %w", req.URL.Path, err)
		}
		retry.Body = body
	}
	res, data, _, err = c.sendWithToken(retry)
	return res, data, err
}

func (c *Client) sendWithToken(req *http.Request) (*http.Response, []byte, string, error) {
	token, err := c.tokens.Token(req.Context())
	if err != nil {
		return nil, nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, nil, "", fmt.Errorf("zitadel: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxAnswer))
	if err != nil {
		return nil, nil, "", fmt.Errorf("zitadel: reading the answer to %s: %w", req.URL.Path, err)
	}
	return res, data, token, nil
}

var grpcCodes = []string{"ok", "canceled", "unknown", "invalid_argument", CodeDeadlineExceeded, CodeNotFound,
	CodeAlreadyExists, "permission_denied", "resource_exhausted", CodeFailedPrecondition, "aborted",
	"out_of_range", "unimplemented", "internal", "unavailable", "data_loss", CodeUnauthenticated}

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
