// Package zitadel is the admin app's client for the Zitadel APIs it manages:
// organizations, users, authorizations (role assignments) and projects.
//
// It calls the v2 services over the Connect protocol with JSON bodies, so it
// needs no SDK and imports nothing from github.com/zitadel/zitadel (ADR-0003,
// C-03). Messages and field names follow the protos of Zitadel v4.19.2
// (proto/zitadel/{org,user,authorization,project}/v2). Zitadel encodes them
// with protojson: field names in lowerCamelCase, 64-bit integers as strings,
// enums by name, and fields with their default value left out.
//
// Branding (label policies and their assets) has no v2 write API in
// v4.19.2, so branding.go calls the v1 REST gateway and the assets API.
//
// The client calls as the admin app's service account, with short-lived
// access tokens it gets from the account's JWT profile key (KeyTokens), never
// with the signed-in admin's token.
package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	orgService           = "zitadel.org.v2.OrganizationService"
	userService          = "zitadel.user.v2.UserService"
	authorizationService = "zitadel.authorization.v2.AuthorizationService"
	projectService       = "zitadel.project.v2.ProjectService"
)

// Connect error codes (https://connectrpc.com/docs/protocol#error-codes)
// that callers act on.
const (
	CodeInvalidArgument    = "invalid_argument"
	CodeNotFound           = "not_found"
	CodeAlreadyExists      = "already_exists"
	CodePermissionDenied   = "permission_denied"
	CodeFailedPrecondition = "failed_precondition"
	CodeAborted            = "aborted"
	CodeOutOfRange         = "out_of_range"
	CodeUnimplemented      = "unimplemented"
	CodeUnavailable        = "unavailable"
	CodeDeadlineExceeded   = "deadline_exceeded"
	CodeUnauthenticated    = "unauthenticated"
)

// maxAnswer bounds what the client reads from one answer of Zitadel.
const maxAnswer = 4 << 20

// Client calls Zitadel as the admin app's service account.
type Client struct {
	baseURL string
	tokens  Tokens
	http    *http.Client

	mu      sync.Mutex
	account *ServiceAccount
}

// NewClient returns a client for the Zitadel instance at baseURL (the
// issuer). httpClient carries the requests; the admin app passes the issuer
// client, which dials the in-cluster Service but keeps the public Host.
// tokens gives the access token of every call.
func NewClient(baseURL string, tokens Tokens, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), tokens: tokens, http: httpClient}
}

// Error is an error answer of Zitadel, with its Connect code and message.
type Error struct {
	Status  int    // HTTP status of the answer
	Code    string // Connect code, such as not_found
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("zitadel: %s: %s", e.Code, e.Message)
}

// HasCode reports whether err is an error answer of Zitadel with the code.
func HasCode(err error, code string) bool {
	var zerr *Error
	return errors.As(err, &zerr) && zerr.Code == code
}

func notFound(what string) error {
	return &Error{Status: http.StatusNotFound, Code: CodeNotFound, Message: what + " not found"}
}

// call sends one unary Connect request, POST /<service>/<method> with the
// request message as JSON, and decodes the answer into resp.
func (c *Client) call(ctx context.Context, service, method string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+service+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Connect-Protocol-Version", "1")
	return c.do(httpReq, resp)
}

func (c *Client) do(req *http.Request, resp any) error {
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

// send makes the request with the client's access token and reads the whole
// answer. When Zitadel answers 401, the token is dropped and the request is
// sent once more with a new one (a token revoked or expired early).
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

// grpcCodes names the numeric gRPC codes of the v1 REST gateway's errors.
var grpcCodes = []string{"ok", "canceled", "unknown", CodeInvalidArgument, CodeDeadlineExceeded, CodeNotFound,
	CodeAlreadyExists, CodePermissionDenied, "resource_exhausted", CodeFailedPrecondition, CodeAborted,
	CodeOutOfRange, CodeUnimplemented, "internal", CodeUnavailable, "data_loss", CodeUnauthenticated}

// parseError reads a Connect error ({"code": "not_found", "message": ...}),
// or a gRPC gateway error with a numeric code. Without either, the code comes
// from the HTTP status as the Connect protocol defines it.
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
	return &Error{Status: status, Code: codeFromStatus(status), Message: http.StatusText(status)}
}

// codeFromStatus is https://connectrpc.com/docs/protocol#http-to-error-code.
func codeFromStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "internal"
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusForbidden:
		return CodePermissionDenied
	case http.StatusNotFound:
		return CodeUnimplemented
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return CodeUnavailable
	default:
		return "unknown"
	}
}

// Uint64 is a protojson 64-bit integer, which is a JSON string but may also
// be a number.
type Uint64 uint64

func (u *Uint64) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	v, err := strconv.ParseUint(strings.Trim(string(b), `"`), 10, 64)
	if err != nil {
		return fmt.Errorf("zitadel: %s is not a uint64", b)
	}
	*u = Uint64(v)
	return nil
}

// Details are object.v2.Details of organizations and users (and the v1
// ObjectDetails, which have the same JSON names). ResourceOwner is the ID of
// the organization the object belongs to.
type Details struct {
	Sequence      Uint64    `json:"sequence"`
	CreationDate  time.Time `json:"creationDate"`
	ChangeDate    time.Time `json:"changeDate"`
	ResourceOwner string    `json:"resourceOwner"`
}

// Page selects up to Limit items after the first Offset items of a list.
type Page struct {
	Offset int
	Limit  int
}

// pagination is object.v2.ListQuery and filter.v2.PaginationRequest, which
// have the same fields.
type pagination struct {
	Offset uint64 `json:"offset,omitempty"`
	Limit  uint32 `json:"limit,omitempty"`
	Asc    bool   `json:"asc,omitempty"`
}

func (p Page) ascending() *pagination {
	return &pagination{Offset: uint64(max(p.Offset, 0)), Limit: uint32(min(max(p.Limit, 0), math.MaxUint32)), Asc: true}
}

// Text query methods: object.v2.TextQueryMethod (org and user v2) and
// filter.v2.TextFilterMethod (project and authorization v2).
const (
	textContainsIgnoreCase   = "TEXT_QUERY_METHOD_CONTAINS_IGNORE_CASE"
	textEquals               = "TEXT_QUERY_METHOD_EQUALS"
	filterContainsIgnoreCase = "TEXT_FILTER_METHOD_CONTAINS_IGNORE_CASE"
)

type idFilter struct {
	ID string `json:"id"`
}

type idsFilter struct {
	IDs []string `json:"ids"`
}

// ServiceAccount is the Zitadel user the client calls as.
type ServiceAccount struct {
	UserID         string
	OrganizationID string
}

// ServiceAccount returns the user of the client's token, from the v1 auth
// API (GET /auth/v1/users/me, which has no v2 equivalent). The answer is
// kept; a failed lookup is tried again on the next call.
func (c *Client) ServiceAccount(ctx context.Context) (ServiceAccount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.account != nil {
		return *c.account, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/auth/v1/users/me", nil)
	if err != nil {
		return ServiceAccount{}, err
	}
	var resp struct {
		User struct {
			ID      string  `json:"id"`
			Details Details `json:"details"`
		} `json:"user"`
	}
	if err := c.do(req, &resp); err != nil {
		return ServiceAccount{}, err
	}
	if resp.User.ID == "" {
		return ServiceAccount{}, errors.New("zitadel: the token's user has no ID")
	}
	c.account = &ServiceAccount{UserID: resp.User.ID, OrganizationID: resp.User.Details.ResourceOwner}
	return *c.account, nil
}
