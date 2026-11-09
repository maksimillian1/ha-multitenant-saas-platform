// Package fakes stands in for Zitadel and Kubernetes in tests of the
// lifecycle and the API. It is imported by tests only.
package fakes

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
)

// Deps stands in for Zitadel and Kubernetes: it keeps what exists, logs
// every call, and fails or blocks a call when told to.
type Deps struct {
	Mu         sync.Mutex
	Orgs       map[string]bool // id → active
	OrgNames   map[string]string
	Admins     map[string]string // user id → org id
	Namespaces map[string]bool
	Calls      []string
	// failNext[method] fails the next n calls of method; lostAnswer makes
	// the call take effect and still fail, as if its answer were lost.
	FailNext   map[string]int
	LostAnswer map[string]bool
	FailWith   error
	Block      map[string]chan struct{}
}

func New() *Deps {
	return &Deps{
		Orgs: map[string]bool{}, OrgNames: map[string]string{}, Admins: map[string]string{},
		Namespaces: map[string]bool{}, FailNext: map[string]int{}, LostAnswer: map[string]bool{},
		FailWith: errors.New("injected failure"), Block: map[string]chan struct{}{},
	}
}

// ErrTimeout is a client error that reports a timeout.
var ErrTimeout = timeoutError{}

type timeoutError struct{}

func (timeoutError) Error() string { return "deadline exceeded" }
func (timeoutError) Timeout() bool { return true }

// enter logs the call and decides its fate: fail before, fail after, or go.
func (f *Deps) enter(ctx context.Context, method, arg string) (before, after error) {
	f.Mu.Lock()
	f.Calls = append(f.Calls, method+" "+arg)
	ch := f.Block[method]
	f.Mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err(), nil
		}
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.FailNext[method] > 0 {
		f.FailNext[method]--
		if f.LostAnswer[method] {
			return nil, f.FailWith
		}
		return f.FailWith, nil
	}
	return nil, nil
}

func (f *Deps) CreateOrganization(ctx context.Context, id, name string) error {
	before, after := f.enter(ctx, "CreateOrganization", id)
	if before != nil {
		return before
	}
	f.Mu.Lock()
	if _, ok := f.Orgs[id]; !ok {
		f.Orgs[id], f.OrgNames[id] = true, name
	}
	f.Mu.Unlock()
	return after
}

func (f *Deps) CreateAdmin(ctx context.Context, orgID, userID, email string) error {
	before, after := f.enter(ctx, "CreateAdmin", orgID)
	if before != nil {
		return before
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if _, ok := f.Orgs[orgID]; !ok {
		return fmt.Errorf("no organization %s", orgID)
	}
	f.Admins[userID] = orgID
	return after
}

func (f *Deps) DeactivateOrganization(ctx context.Context, id string) error {
	return f.setActive(ctx, "DeactivateOrganization", id, false)
}

func (f *Deps) ActivateOrganization(ctx context.Context, id string) error {
	return f.setActive(ctx, "ActivateOrganization", id, true)
}

func (f *Deps) setActive(ctx context.Context, method, id string, active bool) error {
	before, after := f.enter(ctx, method, id)
	if before != nil {
		return before
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if _, ok := f.Orgs[id]; !ok {
		return fmt.Errorf("no organization %s", id)
	}
	f.Orgs[id] = active
	return after
}

func (f *Deps) DeleteOrganization(ctx context.Context, id string) error {
	before, after := f.enter(ctx, "DeleteOrganization", id)
	if before != nil {
		return before
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	delete(f.Orgs, id)
	delete(f.OrgNames, id)
	for u, org := range f.Admins {
		if org == id {
			delete(f.Admins, u)
		}
	}
	return after
}

func (f *Deps) EnsureNamespace(ctx context.Context, tenantID string) error {
	before, after := f.enter(ctx, "EnsureNamespace", tenantID)
	if before != nil {
		return before
	}
	f.Mu.Lock()
	f.Namespaces[tenantID] = true
	f.Mu.Unlock()
	return after
}

func (f *Deps) DeleteNamespace(ctx context.Context, tenantID string) error {
	before, after := f.enter(ctx, "DeleteNamespace", tenantID)
	if before != nil {
		return before
	}
	f.Mu.Lock()
	delete(f.Namespaces, tenantID)
	f.Mu.Unlock()
	return after
}

func (f *Deps) CallLog() []string {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return append([]string(nil), f.Calls...)
}

func (f *Deps) ResetCalls() {
	f.Mu.Lock()
	f.Calls = nil
	f.Mu.Unlock()
}

// exists reports the organization, admin and namespace of a tenant.
func (f *Deps) Exists(id string) (org, admin, ns bool) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	_, org = f.Orgs[id]
	_, admin = f.Admins[core.Tenant{ID: id}.AdminUserID()]
	return org, admin, f.Namespaces[id]
}

func (f *Deps) Counts() (orgs, admins, namespaces int) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return len(f.Orgs), len(f.Admins), len(f.Namespaces)
}
