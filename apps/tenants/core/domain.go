package core

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ADR-0012, item 3.
type State string

const (
	StateProvisioning State = "provisioning"
	StateActive       State = "active"
	StateSuspended    State = "suspended"
	StateDeleting     State = "deleting"
	StateDeleted      State = "deleted"
)

const (
	PartOrganization = "organization"
	PartAdmin        = "admin"
	PartNamespace    = "namespace"
)

// Deletion runs it backwards.
var ProvisionOrder = []string{PartOrganization, PartAdmin, PartNamespace}

// A deleted tenant is a tombstone: only ID, state, timestamps and actors remain.
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

// Derived from the tenant ID, so a retry finds the user an earlier attempt created.
func (t Tenant) AdminUserID() string { return t.ID + "-admin" }

// ADR-0001, item 3.
func Namespace(tenantID string) string { return "tenant-" + tenantID }

var (
	ErrNotFound  = errors.New("tenant not found")
	ErrNameTaken = errors.New("a tenant with this name exists")
	ErrBusy      = errors.New("another request is changing this tenant")
	ErrState     = errors.New("the tenant is not in a state that allows this")
	// Never adopted or changed.
	ErrForeign = errors.New("an object with this name belongs to someone else")
)

// Lowercase UUID: also a valid tenant_id for the Gateway and part of a namespace name.
var IDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var UserIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24+)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
