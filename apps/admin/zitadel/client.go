// Package zitadel imports nothing from github.com/zitadel/zitadel (ADR-0003, C-03). Messages follow
// the v4.19.2 protos in protojson: lowerCamelCase, 64-bit ints as strings, defaults left out.
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

// https://connectrpc.com/docs/protocol#error-codes
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

const maxAnswer = 4 << 20

type Client struct {
	baseURL string
	tokens  Tokens
	http    *http.Client

	mu      sync.Mutex
	account *ServiceAccount
}

// The admin app passes the issuer client, which dials the in-cluster Service but keeps the public Host.
func NewClient(baseURL string, tokens Tokens, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), tokens: tokens, http: httpClient}
}

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("zitadel: %s: %s", e.Code, e.Message)
}

func HasCode(err error, code string) bool {
	var zerr *Error
	return errors.As(err, &zerr) && zerr.Code == code
}

func notFound(what string) error {
	return &Error{Status: http.StatusNotFound, Code: CodeNotFound, Message: what + " not found"}
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

// On 401 (revoked, or expired early) the token is dropped and the request is sent once more.
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

var grpcCodes = []string{"ok", "canceled", "unknown", CodeInvalidArgument, CodeDeadlineExceeded, CodeNotFound,
	CodeAlreadyExists, CodePermissionDenied, "resource_exhausted", CodeFailedPrecondition, CodeAborted,
	CodeOutOfRange, CodeUnimplemented, "internal", CodeUnavailable, "data_loss", CodeUnauthenticated}

// Without a code in the body, it comes from the HTTP status as the Connect protocol defines.
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

// https://connectrpc.com/docs/protocol#http-to-error-code
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

// protojson writes it as a JSON string but may also use a number.
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

// The v1 ObjectDetails have the same JSON names.
type Details struct {
	Sequence      Uint64    `json:"sequence"`
	CreationDate  time.Time `json:"creationDate"`
	ChangeDate    time.Time `json:"changeDate"`
	ResourceOwner string    `json:"resourceOwner"`
}

type Page struct {
	Offset int
	Limit  int
}

// object.v2.ListQuery and filter.v2.PaginationRequest have the same fields.
type pagination struct {
	Offset uint64 `json:"offset,omitempty"`
	Limit  uint32 `json:"limit,omitempty"`
	Asc    bool   `json:"asc,omitempty"`
}

func (p Page) ascending() *pagination {
	return &pagination{Offset: uint64(max(p.Offset, 0)), Limit: uint32(min(max(p.Limit, 0), math.MaxUint32)), Asc: true}
}

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

type ServiceAccount struct {
	UserID         string
	OrganizationID string
}

// GET /auth/v1/users/me has no v2 equivalent. A failed lookup is retried on the next call.
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
