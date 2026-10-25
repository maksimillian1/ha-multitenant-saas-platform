// Package api serves the ExampleApp HTTP API. The tenant comes only from the
// x-tenant-id header, which the API Gateway sets from the validated JWT and
// never takes from the client (docs/contracts/gateway-headers.md).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/internal/store"
)

const TenantHeader = "x-tenant-id"

type Items interface {
	Create(ctx context.Context, tenantID, name string) (store.Item, error)
	List(ctx context.Context, tenantID string) ([]store.Item, error)
	Get(ctx context.Context, tenantID, id string) (store.Item, error)
	Update(ctx context.Context, tenantID, id, name string) (store.Item, error)
	Delete(ctx context.Context, tenantID, id string) error
}

var (
	uuidRe   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	tenantRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

const maxNameLen = 200

// NewHandler returns the API routes. ready reports whether the database is reachable.
func NewHandler(items Items, ready func(context.Context) error, log *slog.Logger) http.Handler {
	h := &handler{items: items, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /items", h.tenant(h.create))
	mux.HandleFunc("GET /items", h.tenant(h.list))
	mux.HandleFunc("GET /items/{id}", h.tenant(h.get))
	mux.HandleFunc("PUT /items/{id}", h.tenant(h.update))
	mux.HandleFunc("DELETE /items/{id}", h.tenant(h.delete))
	return accessLog(mux, log)
}

type handler struct {
	items Items
	log   *slog.Logger
}

type tenantHandler func(w http.ResponseWriter, r *http.Request, tenantID string)

// tenant rejects a request without a well-formed tenant header. With the
// Gateway in front this never happens; it guards against a misconfigured route.
func (h *handler) tenant(next tenantHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get(TenantHeader)
		if !tenantRe.MatchString(tenantID) {
			writeError(w, http.StatusUnauthorized, "missing tenant")
			return
		}
		next(w, r, tenantID)
	}
}

type itemRequest struct {
	// Only the name is read from the body. Any tenant_id field is ignored.
	Name string `json:"name"`
}

func decodeName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req itemRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return "", false
	}
	if req.Name == "" || len(req.Name) > maxNameLen {
		writeError(w, http.StatusBadRequest, "name must be 1-200 characters")
		return "", false
	}
	return req.Name, true
}

func (h *handler) create(w http.ResponseWriter, r *http.Request, tenantID string) {
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	it, err := h.items.Create(r.Context(), tenantID, name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, it)
}

func (h *handler) list(w http.ResponseWriter, r *http.Request, tenantID string) {
	items, err := h.items.List(r.Context(), tenantID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if items == nil {
		items = []store.Item{}
	}
	writeJSON(w, http.StatusOK, map[string][]store.Item{"items": items})
}

func (h *handler) get(w http.ResponseWriter, r *http.Request, tenantID string) {
	id, ok := itemID(w, r)
	if !ok {
		return
	}
	it, err := h.items.Get(r.Context(), tenantID, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request, tenantID string) {
	id, ok := itemID(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	it, err := h.items.Update(r.Context(), tenantID, id, name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request, tenantID string) {
	id, ok := itemID(w, r)
	if !ok {
		return
	}
	if err := h.items.Delete(r.Context(), tenantID, id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// itemID answers 404 for a malformed ID, the same as for another tenant's ID.
func itemID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "item not found")
		return "", false
	}
	return id, true
}

func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	h.log.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusServiceUnavailable, "storage unavailable")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// accessLog logs every request that reaches ExampleApp. The e2e tests use it to
// prove that rejected requests never get here (AC-5).
func accessLog(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.InfoContext(r.Context(), "request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"tenant_id", r.Header.Get(TenantHeader), "duration_ms", time.Since(start).Milliseconds())
	})
}
