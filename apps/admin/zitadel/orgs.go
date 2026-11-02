package zitadel

import "context"

// Organization is an org.v2 Organization. One organization is one tenant
// (ADR-0003).
type Organization struct {
	ID            string            `json:"id"`
	Details       Details           `json:"details"`
	State         OrganizationState `json:"state"`
	Name          string            `json:"name"`
	PrimaryDomain string            `json:"primaryDomain"`
}

// OrganizationState is org.v2.OrganizationState.
type OrganizationState string

const (
	OrganizationActive   OrganizationState = "ORGANIZATION_STATE_ACTIVE"
	OrganizationInactive OrganizationState = "ORGANIZATION_STATE_INACTIVE"
	OrganizationRemoved  OrganizationState = "ORGANIZATION_STATE_REMOVED"
)

// OrganizationFilter narrows ListOrganizations. Zero fields match every
// organization; set fields must all match.
type OrganizationFilter struct {
	Name    string // part of the name, in any case
	State   OrganizationState
	ID      string
	Default bool // only the instance's default organization
}

type OrganizationList struct {
	Total         uint64
	Organizations []Organization
}

// ListOrganizations returns organizations sorted by name.
func (c *Client) ListOrganizations(ctx context.Context, f OrganizationFilter, p Page) (OrganizationList, error) {
	type nameQuery struct {
		Name   string `json:"name"`
		Method string `json:"method"`
	}
	type stateQuery struct {
		State OrganizationState `json:"state"`
	}
	type searchQuery struct {
		NameQuery    *nameQuery  `json:"nameQuery,omitempty"`
		StateQuery   *stateQuery `json:"stateQuery,omitempty"`
		IDQuery      *idFilter   `json:"idQuery,omitempty"`
		DefaultQuery *struct{}   `json:"defaultQuery,omitempty"`
	}
	req := struct {
		Query         *pagination   `json:"query"`
		SortingColumn string        `json:"sortingColumn"`
		Queries       []searchQuery `json:"queries,omitempty"`
	}{Query: p.ascending(), SortingColumn: "ORGANIZATION_FIELD_NAME_NAME"}
	if f.Name != "" {
		req.Queries = append(req.Queries, searchQuery{NameQuery: &nameQuery{Name: f.Name, Method: textContainsIgnoreCase}})
	}
	if f.State != "" {
		req.Queries = append(req.Queries, searchQuery{StateQuery: &stateQuery{State: f.State}})
	}
	if f.ID != "" {
		req.Queries = append(req.Queries, searchQuery{IDQuery: &idFilter{ID: f.ID}})
	}
	if f.Default {
		req.Queries = append(req.Queries, searchQuery{DefaultQuery: &struct{}{}})
	}

	var resp struct {
		Details struct {
			TotalResult Uint64 `json:"totalResult"`
		} `json:"details"`
		Result []Organization `json:"result"`
	}
	if err := c.call(ctx, orgService, "ListOrganizations", req, &resp); err != nil {
		return OrganizationList{}, err
	}
	return OrganizationList{Total: uint64(resp.Details.TotalResult), Organizations: resp.Result}, nil
}

// GetOrganization returns one organization. org.v2 has no get method, so it
// lists by ID.
func (c *Client) GetOrganization(ctx context.Context, id string) (Organization, error) {
	list, err := c.ListOrganizations(ctx, OrganizationFilter{ID: id}, Page{Limit: 1})
	if err != nil {
		return Organization{}, err
	}
	if len(list.Organizations) == 0 || list.Organizations[0].State == OrganizationRemoved {
		return Organization{}, notFound("organization")
	}
	return list.Organizations[0], nil
}

// DefaultOrganizationID returns the ID of the instance's default
// organization, or "" if there is none.
func (c *Client) DefaultOrganizationID(ctx context.Context) (string, error) {
	list, err := c.ListOrganizations(ctx, OrganizationFilter{Default: true}, Page{Limit: 1})
	if err != nil || len(list.Organizations) == 0 {
		return "", err
	}
	return list.Organizations[0].ID, nil
}

// AddOrganization creates an organization without admin users; instance
// administrators, such as the service account, can still manage it.
func (c *Client) AddOrganization(ctx context.Context, name string) (string, error) {
	req := struct {
		Name string `json:"name"`
	}{Name: name}
	var resp struct {
		OrganizationID string `json:"organizationId"`
	}
	if err := c.call(ctx, orgService, "AddOrganization", req, &resp); err != nil {
		return "", err
	}
	return resp.OrganizationID, nil
}

func (c *Client) UpdateOrganization(ctx context.Context, id, name string) error {
	req := struct {
		OrganizationID string `json:"organizationId"`
		Name           string `json:"name"`
	}{OrganizationID: id, Name: name}
	return c.call(ctx, orgService, "UpdateOrganization", req, nil)
}

// DeactivateOrganization stops the organization's users from signing in.
func (c *Client) DeactivateOrganization(ctx context.Context, id string) error {
	return c.call(ctx, orgService, "DeactivateOrganization", organizationRequest{OrganizationID: id}, nil)
}

func (c *Client) ActivateOrganization(ctx context.Context, id string) error {
	return c.call(ctx, orgService, "ActivateOrganization", organizationRequest{OrganizationID: id}, nil)
}

// DeleteOrganization deletes the organization with its users, projects and
// grants in Zitadel. Data of the tenant in other stores stays.
func (c *Client) DeleteOrganization(ctx context.Context, id string) error {
	return c.call(ctx, orgService, "DeleteOrganization", organizationRequest{OrganizationID: id}, nil)
}

type organizationRequest struct {
	OrganizationID string `json:"organizationId"`
}
