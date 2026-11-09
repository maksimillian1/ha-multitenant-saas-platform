package lifecycle

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/internal/fakes"
)

var ctx = context.Background()

func mustCreate(t *testing.T, s *Service, name string) core.Tenant {
	t.Helper()
	tn, err := s.Create(ctx, name, "boss@"+name+".test", "admin-1")
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return tn
}

func assertStepErr(t *testing.T, err error) {
	t.Helper()
	if se := stepErr(t, err); se == nil {
		t.Fatal("no step error")
	}
}

func stepErr(t *testing.T, err error) *StepError {
	t.Helper()
	var se *StepError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a StepError", err)
	}
	return se
}

// Verifies: tenant-management AC-1, AC-2
func TestProvisionStepsInOrder(t *testing.T) {
	s, deps := newService(t)
	a := mustCreate(t, s, "acme")

	if a.State != core.StateActive || a.LastError != "" {
		t.Fatalf("tenant = %+v", a)
	}
	if !slices.Equal(a.Parts, core.ProvisionOrder) {
		t.Fatalf("parts = %v, want %v", a.Parts, core.ProvisionOrder)
	}
	want := []string{"CreateOrganization " + a.ID, "CreateAdmin " + a.ID, "EnsureNamespace " + a.ID}
	if got := deps.CallLog(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if deps.OrgNames[a.ID] != "acme" || deps.Admins[a.AdminUserID()] != a.ID {
		t.Fatalf("organization %q, admin in %q", deps.OrgNames[a.ID], deps.Admins[a.AdminUserID()])
	}
	got, err := s.Get(ctx, a.ID)
	if err != nil || got.State != core.StateActive || !slices.Equal(got.Parts, core.ProvisionOrder) {
		t.Fatalf("stored tenant = %+v, %v", got, err)
	}
}

// Verifies: tenant-management AC-3; T-05
func TestRetryAfterFailureAtEveryStep(t *testing.T) {
	methods := []string{"CreateOrganization", "CreateAdmin", "EnsureNamespace"}
	for i, method := range methods {
		for _, lost := range []bool{false, true} {
			name := method
			if lost {
				name += " answer lost"
			}
			t.Run(name, func(t *testing.T) {
				s, deps := newService(t)
				deps.FailNext[method] = 1
				deps.LostAnswer[method] = lost

				tn, err := s.Create(ctx, "acme", "boss@acme.test", "admin-1")
				se := stepErr(t, err)
				if tn.State != core.StateProvisioning || !slices.Equal(tn.Parts, core.ProvisionOrder[:i]) {
					t.Fatalf("after the failure: state %s, parts %v", tn.State, tn.Parts)
				}
				if se.Tenant.ID != tn.ID || !strings.Contains(tn.LastError, "injected failure") {
					t.Fatalf("last error = %q", tn.LastError)
				}
				stored, _ := s.Get(ctx, tn.ID)
				if stored.LastError != tn.LastError {
					t.Fatalf("stored last error = %q", stored.LastError)
				}

				tn, err = s.Retry(ctx, tn.ID, "admin-2")
				if err != nil {
					t.Fatal(err)
				}
				if tn.State != core.StateActive || tn.LastError != "" {
					t.Fatalf("after retry: %+v", tn)
				}
				if o, a, n := deps.Counts(); o != 1 || a != 1 || n != 1 {
					t.Fatalf("orgs %d, admins %d, namespaces %d; want one each", o, a, n)
				}
				if _, err := s.Retry(ctx, tn.ID, "admin-2"); !errors.Is(err, core.ErrState) {
					t.Fatalf("retry of an active tenant: err = %v", err)
				}
			})
		}
	}
}

// Verifies: tenant-management AC-4; T-06
func TestDependencyFailureStopsAndRecords(t *testing.T) {
	t.Run("Zitadel times out", func(t *testing.T) {
		s, deps := newService(t)
		deps.Block["CreateOrganization"] = make(chan struct{}) // never answers
		start := time.Now()
		tn, err := s.Create(ctx, "acme", "boss@acme.test", "admin-1")
		se := stepErr(t, err)
		if !se.Timeout() {
			t.Fatalf("a hanging Zitadel is not a timeout: %v", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("create took %s; the step timeout is %s", d, s.StepTimeout)
		}
		if tn.State != core.StateProvisioning || len(tn.Parts) != 0 || tn.LastError == "" {
			t.Fatalf("tenant = %+v", tn)
		}
		if got := deps.CallLog(); !slices.Equal(got, []string{"CreateOrganization " + tn.ID}) {
			t.Fatalf("calls after the failure = %v", got)
		}
	})
	t.Run("Kubernetes fails", func(t *testing.T) {
		s, deps := newService(t)
		deps.FailNext["EnsureNamespace"] = 1
		tn, err := s.Create(ctx, "acme", "boss@acme.test", "admin-1")
		if se := stepErr(t, err); se.Timeout() || se.Step != core.PartNamespace {
			t.Fatalf("step error = %+v", se)
		}
		if tn.State != core.StateProvisioning || !slices.Equal(tn.Parts, []string{core.PartOrganization, core.PartAdmin}) {
			t.Fatalf("tenant = %+v", tn)
		}
	})
	t.Run("timeout error of a client", func(t *testing.T) {
		s, deps := newService(t)
		deps.FailNext["CreateAdmin"] = 1
		deps.FailWith = fakes.ErrTimeout
		_, err := s.Create(ctx, "acme", "boss@acme.test", "admin-1")
		if !stepErr(t, err).Timeout() {
			t.Fatal("a client's timeout error is not reported as a timeout")
		}
	})
}

// Verifies: tenant-management AC-6, AC-5; T-04
func TestSuspendResume(t *testing.T) {
	s, deps := newService(t)
	a := mustCreate(t, s, "acme")
	b := mustCreate(t, s, "globex")

	if _, err := s.Resume(ctx, a.ID, "admin-1"); !errors.Is(err, core.ErrState) {
		t.Fatalf("resume of an active tenant: err = %v", err)
	}
	deps.ResetCalls()
	a, err := s.Suspend(ctx, a.ID, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.State != core.StateSuspended || deps.Orgs[a.ID] || !deps.Orgs[b.ID] {
		t.Fatalf("after suspend: state %s, A active %v, B active %v", a.State, deps.Orgs[a.ID], deps.Orgs[b.ID])
	}
	if got := deps.CallLog(); !slices.Equal(got, []string{"DeactivateOrganization " + a.ID}) {
		t.Fatalf("calls = %v", got)
	}
	deps.ResetCalls()
	if _, err := s.Suspend(ctx, a.ID, "admin-1"); !errors.Is(err, core.ErrState) {
		t.Fatalf("suspend of a suspended tenant: err = %v", err)
	}
	if got := deps.CallLog(); len(got) != 0 {
		t.Fatalf("a refused change called %v", got)
	}

	a, err = s.Resume(ctx, a.ID, "admin-1")
	if err != nil || a.State != core.StateActive || !deps.Orgs[a.ID] {
		t.Fatalf("after resume: %+v, %v", a, err)
	}

	// A failed deactivation leaves the tenant active, with the error.
	deps.FailNext["DeactivateOrganization"] = 1
	a, err = s.Suspend(ctx, a.ID, "admin-1")
	assertStepErr(t, err)
	if a.State != core.StateActive || a.LastError == "" {
		t.Fatalf("after a failed suspend: %+v", a)
	}

	// A second request on the same tenant while the first is in a step.
	s.StepTimeout = 10 * time.Second
	deps.ResetCalls()
	release := make(chan struct{})
	deps.Block["DeactivateOrganization"] = release
	done := make(chan error, 1)
	go func() {
		_, err := s.Suspend(ctx, a.ID, "admin-1")
		done <- err
	}()
	for !slices.Contains(deps.CallLog(), "DeactivateOrganization "+a.ID) {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := s.Suspend(ctx, a.ID, "admin-2"); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("concurrent suspend: err = %v, want ErrBusy", err)
	}
	deps.Mu.Lock()
	delete(deps.Block, "DeactivateOrganization")
	deps.Mu.Unlock()
	if _, err := s.Suspend(ctx, b.ID, "admin-2"); err != nil {
		t.Fatalf("suspend of another tenant meanwhile: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Verifies: tenant-management AC-7; T-01
func TestDeleteLeavesTombstoneAndOtherTenants(t *testing.T) {
	s, deps := newService(t)
	a := mustCreate(t, s, "acme")
	b := mustCreate(t, s, "globex")

	if _, err := s.Delete(ctx, a.ID, "admin-2"); !errors.Is(err, core.ErrState) {
		t.Fatalf("delete of an active tenant: err = %v", err)
	}
	if org, admin, ns := deps.Exists(a.ID); !org || !admin || !ns {
		t.Fatal("a refused delete removed something")
	}
	if _, err := s.Suspend(ctx, a.ID, "admin-1"); err != nil {
		t.Fatal(err)
	}

	// A failing step leaves the tenant in deleting; a retry finishes it.
	deps.FailNext["DeleteOrganization"] = 1
	a, err := s.Delete(ctx, a.ID, "admin-2")
	assertStepErr(t, err)
	if a.State != core.StateDeleting || a.LastError == "" || slices.Contains(a.Parts, core.PartNamespace) {
		t.Fatalf("after a failed delete: %+v", a)
	}
	if _, err := s.Resume(ctx, a.ID, "admin-1"); !errors.Is(err, core.ErrState) {
		t.Fatalf("resume of a deleting tenant: err = %v", err)
	}
	a, err = s.Retry(ctx, a.ID, "admin-3")
	if err != nil {
		t.Fatal(err)
	}

	if a.State != core.StateDeleted || a.DeletedAt == nil || a.DeletedBy != "admin-2" ||
		a.Name != "" || a.AdminEmail != "" || len(a.Parts) != 0 || a.CreatedBy != "admin-1" {
		t.Fatalf("tombstone = %+v", a)
	}
	if org, admin, ns := deps.Exists(a.ID); org || admin || ns {
		t.Fatalf("left behind: organization %v, admin %v, namespace %v", org, admin, ns)
	}
	if org, admin, ns := deps.Exists(b.ID); !org || !admin || !ns {
		t.Fatalf("tenant B lost: organization %v, admin %v, namespace %v", org, admin, ns)
	}
	for _, c := range deps.CallLog() {
		if strings.HasSuffix(c, b.ID) && strings.HasPrefix(c, "Delete") {
			t.Fatalf("deleting A called %s", c)
		}
	}
	if _, err := s.Delete(ctx, a.ID, "admin-2"); !errors.Is(err, core.ErrState) {
		t.Fatalf("delete of a deleted tenant: err = %v", err)
	}
	if _, err := s.Retry(ctx, a.ID, "admin-2"); !errors.Is(err, core.ErrState) {
		t.Fatalf("retry of a deleted tenant: err = %v", err)
	}
}

// Verifies: tenant-management AC-7, AC-3; T-05
func TestDeleteStuckProvisioningRemovesUnrecordedParts(t *testing.T) {
	s, deps := newService(t)
	// The organization is created but its answer is lost: the row does not
	// record it, yet it exists.
	deps.FailNext["CreateOrganization"] = 1
	deps.LostAnswer["CreateOrganization"] = true
	a, err := s.Create(ctx, "acme", "boss@acme.test", "admin-1")
	assertStepErr(t, err)
	if len(a.Parts) != 0 {
		t.Fatalf("parts = %v", a.Parts)
	}
	if org, _, _ := deps.Exists(a.ID); !org {
		t.Fatal("the fake did not create the organization")
	}

	a, err = s.Delete(ctx, a.ID, "admin-2")
	if err != nil {
		t.Fatal(err)
	}
	if a.State != core.StateDeleted {
		t.Fatalf("state = %s", a.State)
	}
	if org, _, _ := deps.Exists(a.ID); org {
		t.Fatal("the unrecorded organization is left behind")
	}
}

// Verifies: tenant-management AC-8
func TestNameTakenCreatesNothing(t *testing.T) {
	s, deps := newService(t)
	mustCreate(t, s, "acme")
	deps.ResetCalls()
	if _, err := s.Create(ctx, "ACME", "x@y.test", "admin-1"); !errors.Is(err, core.ErrNameTaken) {
		t.Fatalf("err = %v", err)
	}
	if got := deps.CallLog(); len(got) != 0 {
		t.Fatalf("a refused create called %v", got)
	}
}
