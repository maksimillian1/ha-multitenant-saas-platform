// Package lifecycle records each step before the next; a failed step rolls nothing back.
// Retry resumes at the first step not done, Delete removes whatever exists (ADR-0012).
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
)

type Store interface {
	Create(ctx context.Context, t core.Tenant) (core.Tenant, error)
	Get(ctx context.Context, id string) (core.Tenant, error)
	List(ctx context.Context, includeDeleted bool) ([]core.Tenant, error)
	Save(ctx context.Context, t core.Tenant) (core.Tenant, error)
	Lock(ctx context.Context, id string) (unlock func(), err error)
}

// Every method is idempotent.
type Zitadel interface {
	CreateOrganization(ctx context.Context, id, name string) error
	CreateAdmin(ctx context.Context, orgID, userID, email string) error
	DeactivateOrganization(ctx context.Context, id string) error
	ActivateOrganization(ctx context.Context, id string) error
	DeleteOrganization(ctx context.Context, id string) error
}

// Both methods are idempotent.
type Kube interface {
	EnsureNamespace(ctx context.Context, tenantID string) error
	DeleteNamespace(ctx context.Context, tenantID string) error
}

type StepError struct {
	Tenant core.Tenant
	Step   string
	Err    error
}

func (e *StepError) Error() string { return fmt.Sprintf("step %s: %v", e.Step, e.Err) }
func (e *StepError) Unwrap() error { return e.Err }

func (e *StepError) Timeout() bool {
	var t interface{ Timeout() bool }
	return errors.Is(e.Err, context.DeadlineExceeded) || (errors.As(e.Err, &t) && t.Timeout())
}

type Service struct {
	store   Store
	zitadel Zitadel
	kube    Kube
	log     *slog.Logger

	// DeleteTimeout covers waiting until the namespace is gone.
	StepTimeout   time.Duration
	DeleteTimeout time.Duration
	NewID         func() string
	Now           func() time.Time
	// nil publishes nothing.
	Published func(ctx context.Context, t core.Tenant)
}

func NewService(store Store, zitadel Zitadel, kube Kube, log *slog.Logger, newID func() string) *Service {
	return &Service{
		store: store, zitadel: zitadel, kube: kube, log: log,
		StepTimeout: 15 * time.Second, DeleteTimeout: 3 * time.Minute,
		NewID: newID, Now: time.Now,
	}
}

func (s *Service) Get(ctx context.Context, id string) (core.Tenant, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, includeDeleted bool) ([]core.Tenant, error) {
	return s.store.List(ctx, includeDeleted)
}

func (s *Service) Create(ctx context.Context, name, adminEmail, admin string) (core.Tenant, error) {
	ctx = context.WithoutCancel(ctx) // a started provisioning finishes even if the caller leaves
	t, err := s.store.Create(ctx, core.Tenant{ID: s.NewID(), Name: name, AdminEmail: adminEmail, CreatedBy: admin})
	if err != nil {
		return core.Tenant{}, err
	}
	unlock, err := s.store.Lock(ctx, t.ID)
	if err != nil {
		return t, err
	}
	defer unlock()
	t, err = s.provision(ctx, t)
	s.audit(ctx, "create", admin, t, err)
	s.publish(ctx, t)
	return t, err
}

func (s *Service) Retry(ctx context.Context, id, admin string) (core.Tenant, error) {
	ctx = context.WithoutCancel(ctx)
	return s.locked(ctx, id, "retry", admin, func(t core.Tenant) (core.Tenant, error) {
		switch t.State {
		case core.StateProvisioning:
			return s.provision(ctx, t)
		case core.StateDeleting:
			return s.deprovision(ctx, t)
		default:
			return t, core.ErrState
		}
	})
}

// Its data stays.
func (s *Service) Suspend(ctx context.Context, id, admin string) (core.Tenant, error) {
	ctx = context.WithoutCancel(ctx)
	return s.locked(ctx, id, "suspend", admin, func(t core.Tenant) (core.Tenant, error) {
		if t.State != core.StateActive {
			return t, core.ErrState
		}
		return s.switchState(ctx, t, "deactivate organization", s.zitadel.DeactivateOrganization, core.StateSuspended)
	})
}

func (s *Service) Resume(ctx context.Context, id, admin string) (core.Tenant, error) {
	ctx = context.WithoutCancel(ctx)
	return s.locked(ctx, id, "resume", admin, func(t core.Tenant) (core.Tenant, error) {
		if t.State != core.StateSuspended {
			return t, core.ErrState
		}
		return s.switchState(ctx, t, "activate organization", s.zitadel.ActivateOrganization, core.StateActive)
	})
}

func (s *Service) Delete(ctx context.Context, id, admin string) (core.Tenant, error) {
	ctx = context.WithoutCancel(ctx)
	return s.locked(ctx, id, "delete", admin, func(t core.Tenant) (core.Tenant, error) {
		switch t.State {
		case core.StateSuspended, core.StateProvisioning:
			t.State, t.DeletedBy = core.StateDeleting, admin
			saved, err := s.store.Save(ctx, t)
			if err != nil {
				return t, err
			}
			return s.deprovision(ctx, saved)
		case core.StateDeleting:
			return s.deprovision(ctx, t)
		default:
			return t, core.ErrState
		}
	})
}

func (s *Service) locked(ctx context.Context, id, action, admin string, fn func(core.Tenant) (core.Tenant, error)) (core.Tenant, error) {
	unlock, err := s.store.Lock(ctx, id)
	if err != nil {
		return core.Tenant{}, err
	}
	defer unlock()
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return core.Tenant{}, err
	}
	t, err = fn(t)
	s.audit(ctx, action, admin, t, err)
	s.publish(ctx, t)
	return t, err
}

func (s *Service) publish(ctx context.Context, t core.Tenant) {
	if s.Published != nil && t.ID != "" {
		s.Published(ctx, t)
	}
}

func (s *Service) provision(ctx context.Context, t core.Tenant) (core.Tenant, error) {
	for _, part := range core.ProvisionOrder {
		if slices.Contains(t.Parts, part) {
			continue
		}
		var err error
		switch part {
		case core.PartOrganization:
			err = s.step(ctx, s.StepTimeout, func(ctx context.Context) error {
				return s.zitadel.CreateOrganization(ctx, t.ID, t.Name)
			})
		case core.PartAdmin:
			err = s.step(ctx, s.StepTimeout, func(ctx context.Context) error {
				return s.zitadel.CreateAdmin(ctx, t.ID, t.AdminUserID(), t.AdminEmail)
			})
		case core.PartNamespace:
			err = s.step(ctx, s.StepTimeout, func(ctx context.Context) error {
				return s.kube.EnsureNamespace(ctx, t.ID)
			})
		}
		if err != nil {
			return s.failed(ctx, t, part, err)
		}
		t.Parts = append(t.Parts, part)
		t.LastError = ""
		if t, err = s.store.Save(ctx, t); err != nil {
			return t, err
		}
	}
	t.State = core.StateActive
	return s.store.Save(ctx, t)
}

// Every part, recorded or not: an attempt that died before recording must not leave an orphan.
func (s *Service) deprovision(ctx context.Context, t core.Tenant) (core.Tenant, error) {
	if err := s.step(ctx, s.DeleteTimeout, func(ctx context.Context) error {
		return s.kube.DeleteNamespace(ctx, t.ID)
	}); err != nil {
		return s.failed(ctx, t, "delete "+core.PartNamespace, err)
	}
	t.Parts = slices.DeleteFunc(t.Parts, func(p string) bool { return p == core.PartNamespace })
	var err error
	if t, err = s.store.Save(ctx, t); err != nil {
		return t, err
	}

	// Deleting the organization deletes its users, the tenant's admin with it.
	if err := s.step(ctx, s.StepTimeout, func(ctx context.Context) error {
		return s.zitadel.DeleteOrganization(ctx, t.ID)
	}); err != nil {
		return s.failed(ctx, t, "delete "+core.PartOrganization, err)
	}
	now := s.Now()
	t.State, t.Parts, t.LastError, t.DeletedAt = core.StateDeleted, []string{}, "", &now
	return s.store.Save(ctx, t)
}

func (s *Service) switchState(ctx context.Context, t core.Tenant, step string, call func(context.Context, string) error, to core.State) (core.Tenant, error) {
	if err := s.step(ctx, s.StepTimeout, func(ctx context.Context) error { return call(ctx, t.ID) }); err != nil {
		return s.failed(ctx, t, step, err)
	}
	t.State, t.LastError = to, ""
	return s.store.Save(ctx, t)
}

func (s *Service) step(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn(ctx)
}

func (s *Service) failed(ctx context.Context, t core.Tenant, step string, err error) (core.Tenant, error) {
	t.LastError = fmt.Sprintf("%s: %v", step, err)
	if saved, saveErr := s.store.Save(ctx, t); saveErr == nil {
		t = saved
	} else {
		s.log.ErrorContext(ctx, "recording a failed step", "tenant_id", t.ID, "err", saveErr)
	}
	return t, &StepError{Tenant: t, Step: step, Err: err}
}

func (s *Service) audit(ctx context.Context, action, admin string, t core.Tenant, err error) {
	attrs := []any{"action", action, "admin", admin, "tenant_id", t.ID, "state", t.State}
	if err != nil {
		s.log.WarnContext(ctx, "tenant change failed", append(attrs, "err", err)...)
		return
	}
	s.log.InfoContext(ctx, "tenant changed", attrs...)
}
