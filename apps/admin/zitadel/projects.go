package zitadel

import (
	"context"
	"time"
)

// Project is a project.v2 Project. Granted projects carry the organization
// they are granted to.
type Project struct {
	ID                      string       `json:"projectId"`
	OrganizationID          string       `json:"organizationId"`
	CreationDate            time.Time    `json:"creationDate"`
	ChangeDate              time.Time    `json:"changeDate"`
	Name                    string       `json:"name"`
	State                   ProjectState `json:"state"`
	ProjectRoleAssertion    bool         `json:"projectRoleAssertion"`
	AuthorizationRequired   bool         `json:"authorizationRequired"`
	ProjectAccessRequired   bool         `json:"projectAccessRequired"`
	GrantedOrganizationID   string       `json:"grantedOrganizationId"`
	GrantedOrganizationName string       `json:"grantedOrganizationName"`
}

// ProjectState is project.v2.ProjectState.
type ProjectState string

const (
	ProjectActive   ProjectState = "PROJECT_STATE_ACTIVE"
	ProjectInactive ProjectState = "PROJECT_STATE_INACTIVE"
)

// ProjectRole is project.v2.ProjectRole.
type ProjectRole struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Group       string `json:"group"`
}

// ProjectFilter narrows ListProjects; set fields must all match.
type ProjectFilter struct {
	Name           string // part of the name, in any case
	OrganizationID string // owned by this organization
}

type ProjectList struct {
	Total    uint64
	Projects []Project
}

// ListProjects returns projects sorted by name.
func (c *Client) ListProjects(ctx context.Context, f ProjectFilter, p Page) (ProjectList, error) {
	type nameFilter struct {
		ProjectName string `json:"projectName"`
		Method      string `json:"method"`
	}
	type organizationFilter struct {
		OrganizationID string `json:"organizationId"`
		Type           string `json:"type"`
	}
	type searchFilter struct {
		ProjectNameFilter    *nameFilter         `json:"projectNameFilter,omitempty"`
		OrganizationIDFilter *organizationFilter `json:"organizationIdFilter,omitempty"`
	}
	req := struct {
		Pagination    *pagination    `json:"pagination"`
		SortingColumn string         `json:"sortingColumn"`
		Filters       []searchFilter `json:"filters,omitempty"`
	}{Pagination: p.ascending(), SortingColumn: "PROJECT_FIELD_NAME_NAME"}
	if f.Name != "" {
		req.Filters = append(req.Filters, searchFilter{ProjectNameFilter: &nameFilter{ProjectName: f.Name, Method: filterContainsIgnoreCase}})
	}
	if f.OrganizationID != "" {
		req.Filters = append(req.Filters, searchFilter{OrganizationIDFilter: &organizationFilter{OrganizationID: f.OrganizationID, Type: "OWNED"}})
	}

	var resp struct {
		Pagination struct {
			TotalResult Uint64 `json:"totalResult"`
		} `json:"pagination"`
		Projects []Project `json:"projects"`
	}
	if err := c.call(ctx, projectService, "ListProjects", req, &resp); err != nil {
		return ProjectList{}, err
	}
	return ProjectList{Total: uint64(resp.Pagination.TotalResult), Projects: resp.Projects}, nil
}

func (c *Client) GetProject(ctx context.Context, id string) (Project, error) {
	req := struct {
		ProjectID string `json:"projectId"`
	}{ProjectID: id}
	var resp struct {
		Project Project `json:"project"`
	}
	if err := c.call(ctx, projectService, "GetProject", req, &resp); err != nil {
		return Project{}, err
	}
	if resp.Project.ID == "" {
		return Project{}, notFound("project")
	}
	return resp.Project, nil
}

// ListProjectRoles returns the project's roles sorted by key.
func (c *Client) ListProjectRoles(ctx context.Context, projectID string, p Page) ([]ProjectRole, error) {
	req := struct {
		ProjectID     string      `json:"projectId"`
		Pagination    *pagination `json:"pagination"`
		SortingColumn string      `json:"sortingColumn"`
	}{ProjectID: projectID, Pagination: p.ascending(), SortingColumn: "PROJECT_ROLE_FIELD_NAME_KEY"}
	var resp struct {
		ProjectRoles []ProjectRole `json:"projectRoles"`
	}
	if err := c.call(ctx, projectService, "ListProjectRoles", req, &resp); err != nil {
		return nil, err
	}
	return resp.ProjectRoles, nil
}
