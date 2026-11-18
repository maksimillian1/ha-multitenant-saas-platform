package zitadel

import (
	"context"
	"time"
)

// Authorization is an authorization.v2 Authorization: the roles a user holds
// in a project, on behalf of an organization (Zitadel's "role assignment",
// formerly user grant).
type Authorization struct {
	ID           string    `json:"id"`
	CreationDate time.Time `json:"creationDate"`
	ChangeDate   time.Time `json:"changeDate"`
	Project      struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		OrganizationID string `json:"organizationId"`
	} `json:"project"`
	Organization struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"organization"`
	User struct {
		ID                 string `json:"id"`
		PreferredLoginName string `json:"preferredLoginName"`
		DisplayName        string `json:"displayName"`
		OrganizationID     string `json:"organizationId"`
	} `json:"user"`
	State AuthorizationState `json:"state"`
	Roles []Role             `json:"roles"`
}

// AuthorizationState is authorization.v2.State.
type AuthorizationState string

const (
	AuthorizationActive   AuthorizationState = "STATE_ACTIVE"
	AuthorizationInactive AuthorizationState = "STATE_INACTIVE"
)

// Role is authorization.v2.Role.
type Role struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Group       string `json:"group"`
}

// AuthorizationFilter narrows ListAuthorizations; set fields must all match.
type AuthorizationFilter struct {
	IDs       []string
	UserID    string
	ProjectID string
}

type AuthorizationList struct {
	Total          uint64
	Authorizations []Authorization
}

// ListAuthorizations returns authorizations, oldest first.
func (c *Client) ListAuthorizations(ctx context.Context, f AuthorizationFilter, p Page) (AuthorizationList, error) {
	type searchFilter struct {
		AuthorizationIDs *idsFilter `json:"authorizationIds,omitempty"`
		InUserIDs        *idsFilter `json:"inUserIds,omitempty"`
		ProjectID        *idFilter  `json:"projectId,omitempty"`
	}
	req := struct {
		Pagination    *pagination    `json:"pagination"`
		SortingColumn string         `json:"sortingColumn"`
		Filters       []searchFilter `json:"filters,omitempty"`
	}{Pagination: p.ascending(), SortingColumn: "AUTHORIZATION_FIELD_NAME_CREATED_DATE"}
	if len(f.IDs) > 0 {
		req.Filters = append(req.Filters, searchFilter{AuthorizationIDs: &idsFilter{IDs: f.IDs}})
	}
	if f.UserID != "" {
		req.Filters = append(req.Filters, searchFilter{InUserIDs: &idsFilter{IDs: []string{f.UserID}}})
	}
	if f.ProjectID != "" {
		req.Filters = append(req.Filters, searchFilter{ProjectID: &idFilter{ID: f.ProjectID}})
	}

	var resp struct {
		Pagination struct {
			TotalResult Uint64 `json:"totalResult"`
		} `json:"pagination"`
		Authorizations []Authorization `json:"authorizations"`
	}
	if err := c.call(ctx, authorizationService, "ListAuthorizations", req, &resp); err != nil {
		return AuthorizationList{}, err
	}
	return AuthorizationList{Total: uint64(resp.Pagination.TotalResult), Authorizations: resp.Authorizations}, nil
}

// GetAuthorization returns one authorization, listed by ID.
func (c *Client) GetAuthorization(ctx context.Context, id string) (Authorization, error) {
	list, err := c.ListAuthorizations(ctx, AuthorizationFilter{IDs: []string{id}}, Page{Limit: 1})
	if err != nil {
		return Authorization{}, err
	}
	if len(list.Authorizations) == 0 {
		return Authorization{}, notFound("role assignment")
	}
	return list.Authorizations[0], nil
}

// CreateAuthorization gives the user roles in a project. organizationID owns
// the project or has a grant for it.
func (c *Client) CreateAuthorization(ctx context.Context, userID, projectID, organizationID string, roleKeys []string) (string, error) {
	req := struct {
		UserID         string   `json:"userId"`
		ProjectID      string   `json:"projectId"`
		OrganizationID string   `json:"organizationId"`
		RoleKeys       []string `json:"roleKeys"`
	}{UserID: userID, ProjectID: projectID, OrganizationID: organizationID, RoleKeys: roleKeys}
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, authorizationService, "CreateAuthorization", req, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// UpdateAuthorization replaces the roles: keys left out are revoked.
func (c *Client) UpdateAuthorization(ctx context.Context, id string, roleKeys []string) error {
	req := struct {
		ID       string   `json:"id"`
		RoleKeys []string `json:"roleKeys"`
	}{ID: id, RoleKeys: roleKeys}
	return c.call(ctx, authorizationService, "UpdateAuthorization", req, nil)
}

func (c *Client) DeleteAuthorization(ctx context.Context, id string) error {
	req := struct {
		ID string `json:"id"`
	}{ID: id}
	return c.call(ctx, authorizationService, "DeleteAuthorization", req, nil)
}
