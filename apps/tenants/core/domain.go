// Package core holds the tenant lifecycle's types (ADR-0012).
package core

import (
	"errors"
	"regexp"
	"time"
)

// State is where a tenant is in its lifecycle (ADR-0012, item 3).
type State string

const (
	StateProvisioning State = "provisioning"
	StateActive       State = "active"
	StateSuspended    State = "suspended"
	StateDeleting     State = "deleting"
	StateDeleted      State = "deleted"
)

// Parts of a tenant that exist outside the tenants table. A tenant's Parts
// lists those created so far; deletion removes them again.
const (
	PartOrganization = "organization"
	PartAdmin        = "admin"
	PartNamespace    = "namespace"
)

// ProvisionOrder is the order in which the parts are created; deletion runs
// it backwards.
var ProvisionOrder = []string{PartOrganization, PartAdmin, PartNamespace}

// Tenant is one row of the tenant registry. A deleted tenant is a tombstone:
// only its ID, state, timestamps and who created and deleted it remain.
type Tenant struct {
	ID         string     `json:"id"`
	Name       string     `json:"name,omitempty"`
	AdminEmail string     `json:"admin_email,omitempty"`
	State      State      `json:"state"`
	Parts      []string   `json:"parts"`
	LastError  string     `json:"last_error,omitempty"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedBy  string     `json:"deleted_by,omitempty"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}

// AdminUserID is the Zitadel user ID of the tenant's first admin. It is
// derived from the tenant ID, so a retry finds the user an earlier attempt
// created.
func (t Tenant) AdminUserID() string { return t.ID + "-admin" }

// Namespace is the Kubernetes namespace of the tenant's workloads
// (ADR-0001, item 3).
func Namespace(tenantID string) string { return "tenant-" + tenantID }

var (
	ErrNotFound  = errors.New("tenant not found")
	ErrNameTaken = errors.New("a tenant with this name exists")
	ErrBusy      = errors.New("another request is changing this tenant")
	ErrState     = errors.New("the tenant is not in a state that allows this")
)

// IDRe matches a tenant ID: a lowercase UUID, which is also a valid tenant_id
// for the API Gateway and part of a valid namespace name.
var IDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// UserIDRe matches the acting admin's Zitadel user ID.
var UserIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
