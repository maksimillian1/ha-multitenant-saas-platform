// Package items serves the ExampleApp items API. The tenant comes only from
// the x-tenant-id header, which the API Gateway sets from the validated JWT
// and never takes from the client (docs/contracts/gateway-headers.md).
package items

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"unicode/utf8"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/core"
)

const maxNameLen = 200

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Service struct {
	Store core.ItemStore
	Log   *slog.Logger
}

func NewService(store core.ItemStore, log *slog.Logger) *Service {
	return &Service{Store: store, Log: log}
}

type itemRequest struct {
	// Only the name is read from the body. Any tenant_id field is ignored.
	Name string `json:"name"`
}

func (s *Service) CreateHandler(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenant(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	it, err := s.Store.Create(r.Context(), tenantID, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusCreated, it)
}

func (s *Service) ListHandler(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenant(w, r)
	if !ok {
		return
	}
	items, err := s.Store.List(r.Context(), tenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []core.Item{}
	}
	core.WriteJSON(w, http.StatusOK, map[string][]core.Item{"items": items})
}

func (s *Service) GetHandler(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := itemID(w, r)
	if !ok {
		return
	}
	it, err := s.Store.Get(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, it)
}

func (s *Service) UpdateHandler(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := itemID(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	it, err := s.Store.Update(r.Context(), tenantID, id, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, it)
}

func (s *Service) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := itemID(w, r)
	if !ok {
		return
	}
	if err := s.Store.Delete(r.Context(), tenantID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// tenant rejects a request without a well-formed tenant header. With the
// Gateway in front this never happens; it guards against a misconfigured route.
func tenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	tenantID := r.Header.Get(core.TenantHeader)
	if !core.TenantRe.MatchString(tenantID) {
		core.WriteError(w, http.StatusUnauthorized, "missing tenant")
		return "", false
	}
	return tenantID, true
}

func decodeName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req itemRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		core.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return "", false
	}
	if req.Name == "" || utf8.RuneCountInString(req.Name) > maxNameLen {
		core.WriteError(w, http.StatusBadRequest, "name must be 1-200 characters")
		return "", false
	}
	return req.Name, true
}

// itemID answers 404 for a malformed ID, the same as for another tenant's ID.
func itemID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		core.WriteError(w, http.StatusNotFound, "item not found")
		return "", false
	}
	return id, true
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, core.ErrNotFound) {
		core.WriteError(w, http.StatusNotFound, "item not found")
		return
	}
	s.Log.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "err", err)
	core.WriteError(w, http.StatusServiceUnavailable, "storage unavailable")
}
