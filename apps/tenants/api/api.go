package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/lifecycle"
)

// Trusted for the audit log: only a caller with the role tenant-manager gets this far.
const AdminHeader = "X-Admin-User-Id"

type Lifecycle interface {
	Get(ctx context.Context, id string) (core.Tenant, error)
	List(ctx context.Context, includeDeleted bool) ([]core.Tenant, error)
	Create(ctx context.Context, name, adminEmail, admin string) (core.Tenant, error)
	Retry(ctx context.Context, id, admin string) (core.Tenant, error)
	Suspend(ctx context.Context, id, admin string) (core.Tenant, error)
	Resume(ctx context.Context, id, admin string) (core.Tenant, error)
	Delete(ctx context.Context, id, admin string) (core.Tenant, error)
}

type Server struct {
	tenants Lifecycle
	log     *slog.Logger
}

func NewServer(tenants Lifecycle, log *slog.Logger) *Server {
	return &Server{tenants: tenants, log: log}
}

func (s *Server) Routes(checker auth.Checker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tenants", s.list)
	mux.HandleFunc("POST /tenants", s.change(s.create))
	mux.HandleFunc("GET /tenants/{id}", s.withID(s.get))
	mux.HandleFunc("POST /tenants/{id}/retry", s.change(s.withIDChange(s.tenants.Retry)))
	mux.HandleFunc("POST /tenants/{id}/suspend", s.change(s.withIDChange(s.tenants.Suspend)))
	mux.HandleFunc("POST /tenants/{id}/resume", s.change(s.withIDChange(s.tenants.Resume)))
	mux.HandleFunc("DELETE /tenants/{id}", s.change(s.withIDChange(s.tenants.Delete)))
	return auth.Require(checker, mux)
}

type TenantList struct {
	Tenants []core.Tenant `json:"tenants"`
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	includeDeleted := r.URL.Query().Get("include_deleted") == "true"
	list, err := s.tenants.List(r.Context(), includeDeleted)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, TenantList{Tenants: list})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, id string) {
	t, err := s.tenants.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, t)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request, admin string) {
	req, err := DecodeCreate(r.Body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.tenants.Create(r.Context(), req.Name, req.AdminEmail, admin)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusCreated, t)
}

type changeHandler func(w http.ResponseWriter, r *http.Request, admin string)

func (s *Server) change(next changeHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin := r.Header.Get(AdminHeader)
		if !core.UserIDRe.MatchString(admin) {
			core.WriteError(w, http.StatusBadRequest, AdminHeader+" must name the acting admin")
			return
		}
		next(w, r, admin)
	}
}

// 404 for a path ID that cannot be a tenant ID.
func (s *Server) withID(next func(w http.ResponseWriter, r *http.Request, id string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !core.IDRe.MatchString(id) {
			core.WriteError(w, http.StatusNotFound, core.ErrNotFound.Error())
			return
		}
		next(w, r, id)
	}
}

func (s *Server) withIDChange(op func(ctx context.Context, id, admin string) (core.Tenant, error)) changeHandler {
	return func(w http.ResponseWriter, r *http.Request, admin string) {
		s.withID(func(w http.ResponseWriter, r *http.Request, id string) {
			t, err := op(r.Context(), id, admin)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			core.WriteJSON(w, http.StatusOK, t)
		})(w, r)
	}
}

// Every failure answer carries the tenant as the failure left it.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var input *InputError
	var step *lifecycle.StepError
	switch {
	case errors.As(err, &input):
		core.WriteError(w, http.StatusBadRequest, input.Msg)
	case errors.Is(err, core.ErrNotFound):
		core.WriteError(w, http.StatusNotFound, core.ErrNotFound.Error())
	case errors.Is(err, core.ErrNameTaken), errors.Is(err, core.ErrBusy), errors.Is(err, core.ErrState):
		core.WriteError(w, http.StatusConflict, err.Error())
	case errors.As(err, &step):
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, core.ErrForeign):
			status = http.StatusConflict
		case step.Timeout():
			status = http.StatusGatewayTimeout
		}
		t := step.Tenant
		core.WriteJSON(w, status, core.ErrorBody{Error: step.Error(), Tenant: &t})
	default:
		s.log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		core.WriteError(w, http.StatusInternalServerError, "internal error")
	}
}
