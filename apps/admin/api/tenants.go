package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/tenants"
)

// Tenants is what the API needs from Tenant Management; *tenants.Client
// implements it. Every change names the admin who asked for it.
type Tenants interface {
	List(ctx context.Context, includeDeleted bool) ([]tenants.Tenant, error)
	Get(ctx context.Context, id string) (tenants.Tenant, error)
	Create(ctx context.Context, adminID string, in tenants.Create) (tenants.Tenant, error)
	Retry(ctx context.Context, adminID, id string) (tenants.Tenant, error)
	Suspend(ctx context.Context, adminID, id string) (tenants.Tenant, error)
	Resume(ctx context.Context, adminID, id string) (tenants.Tenant, error)
	Delete(ctx context.Context, adminID, id string) (tenants.Tenant, error)
}

var _ Tenants = (*tenants.Client)(nil)

func (s *Service) registerTenants(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tenants", s.read(s.withTenants(s.listTenants)))
	mux.HandleFunc("POST /api/tenants", s.change(s.withTenants(s.createTenant)))
	mux.HandleFunc("GET /api/tenants/{tenantId}", s.read(s.withTenants(s.getTenant)))
	mux.HandleFunc("DELETE /api/tenants/{tenantId}", s.change(s.withTenants(s.tenantChange(Tenants.Delete))))
	mux.HandleFunc("POST /api/tenants/{tenantId}/retry", s.change(s.withTenants(s.tenantChange(Tenants.Retry))))
	mux.HandleFunc("POST /api/tenants/{tenantId}/suspend", s.change(s.withTenants(s.tenantChange(Tenants.Suspend))))
	mux.HandleFunc("POST /api/tenants/{tenantId}/resume", s.change(s.withTenants(s.tenantChange(Tenants.Resume))))
}

// withTenants answers 503 when the app has no Tenant Management configured.
func (s *Service) withTenants(h handler) handler {
	return func(w http.ResponseWriter, r *http.Request, admin core.Session) {
		if s.Tenants == nil {
			core.WriteError(w, http.StatusServiceUnavailable, "Tenant Management is not configured")
			return
		}
		h(w, r, admin)
	}
}

func (s *Service) listTenants(w http.ResponseWriter, r *http.Request, _ core.Session) {
	var includeDeleted bool
	switch r.URL.Query().Get("include_deleted") {
	case "", "false":
	case "true":
		includeDeleted = true
	default:
		badRequest(w, errors.New("include_deleted must be true or false"))
		return
	}
	list, err := s.Tenants.List(r.Context(), includeDeleted)
	if err != nil {
		s.tenantFail(w, r, err)
		return
	}
	items := make([]Tenant, 0, len(list))
	for _, t := range list {
		items = append(items, tenantFrom(t))
	}
	core.WriteJSON(w, http.StatusOK, TenantList{Items: items})
}

func (s *Service) getTenant(w http.ResponseWriter, r *http.Request, _ core.Session) {
	t, err := s.Tenants.Get(r.Context(), r.PathValue("tenantId"))
	if err != nil {
		s.tenantFail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, tenantFrom(t))
}

func (s *Service) createTenant(w http.ResponseWriter, r *http.Request, admin core.Session) {
	var body TenantCreate
	if !decode(w, r, &body) {
		return
	}
	var f fields
	f.text("name", &body.Name, 1, 100)
	f.text("admin_email", &body.AdminEmail, 1, 254)
	if body.AdminEmail != "" && !strings.Contains(body.AdminEmail, "@") {
		f.fail("admin_email is not an email address")
	}
	if err := f.err(); err != nil {
		badRequest(w, err)
		return
	}
	t, err := s.Tenants.Create(r.Context(), admin.Subject, tenants.Create{Name: body.Name, AdminEmail: body.AdminEmail})
	if err != nil {
		s.tenantFail(w, r, err)
		return
	}
	setTarget(w, t.ID)
	core.WriteJSON(w, http.StatusCreated, tenantFrom(t))
}

// tenantChange serves a change of one tenant: retry, suspend, resume or
// delete. Each answers with the tenant after the change.
func (s *Service) tenantChange(call func(Tenants, context.Context, string, string) (tenants.Tenant, error)) handler {
	return func(w http.ResponseWriter, r *http.Request, admin core.Session) {
		t, err := call(s.Tenants, r.Context(), admin.Subject, r.PathValue("tenantId"))
		if err != nil {
			s.tenantFail(w, r, err)
			return
		}
		core.WriteJSON(w, http.StatusOK, tenantFrom(t))
	}
}

// tenantFail answers for a failed call to Tenant Management. Its 400 and 404
// pass through; its 409, 502 and 504 pass through with the tenant as the
// failure left it, so the admin sees the step and the error. An outage is
// 502, a call that took too long 504.
func (s *Service) tenantFail(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	var terr *tenants.Error
	if errors.As(err, &terr) {
		var tenant *Tenant
		if terr.Tenant != nil {
			t := tenantFrom(*terr.Tenant)
			tenant = &t
			setTarget(w, t.Id)
		}
		switch terr.Status {
		case http.StatusBadRequest, http.StatusNotFound:
			core.WriteError(w, terr.Status, terr.Message)
			return
		case http.StatusConflict, http.StatusBadGateway, http.StatusGatewayTimeout:
			if terr.Status != http.StatusConflict {
				s.Log.WarnContext(ctx, "tenant step failed", "status", terr.Status, "err", terr.Message)
			}
			core.WriteJSON(w, terr.Status, TenantError{Error: terr.Message, Tenant: tenant})
			return
		case http.StatusUnauthorized, http.StatusForbidden:
			s.Log.ErrorContext(ctx, "Tenant Management refused the admin app's token", "err", err)
			core.WriteError(w, http.StatusBadGateway, "Tenant Management refused the admin app's machine token")
			return
		}
		s.Log.ErrorContext(ctx, "Tenant Management failed", "err", err)
		core.WriteJSON(w, http.StatusBadGateway, TenantError{Error: "Tenant Management failed", Tenant: tenant})
		return
	}
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		s.Log.DebugContext(ctx, "request canceled", "err", err)
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout():
		s.Log.WarnContext(ctx, "Tenant Management timed out", "err", err)
		core.WriteError(w, http.StatusGatewayTimeout, "Tenant Management did not answer in time")
		return
	default:
		s.Log.ErrorContext(ctx, "Tenant Management call failed", "err", err)
	}
	core.WriteError(w, http.StatusBadGateway, "Tenant Management is unavailable")
}

// setTarget names the tenant in the change log once its ID is known.
func setTarget(w http.ResponseWriter, id string) {
	if a, ok := w.(*auditWriter); ok && id != "" {
		a.target = id
	}
}

func tenantFrom(t tenants.Tenant) Tenant {
	parts := make([]TenantPart, 0, len(t.Parts))
	for _, p := range t.Parts {
		parts = append(parts, TenantPart(p))
	}
	return Tenant{
		Id:         t.ID,
		Name:       optional(t.Name),
		AdminEmail: optional(t.AdminEmail),
		State:      TenantState(t.State),
		Parts:      parts,
		LastError:  optional(t.LastError),
		CreatedBy:  t.CreatedBy,
		CreatedAt:  t.CreatedAt,
		UpdatedAt:  t.UpdatedAt,
		DeletedBy:  optional(t.DeletedBy),
		DeletedAt:  t.DeletedAt,
	}
}

func optional(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
