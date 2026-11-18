package api

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

// fail answers for a refused change or a failed call to Zitadel. Errors the
// admin can act on keep Zitadel's message (it says what is wrong with the
// input or the state); every other error is logged and answered without
// details of Zitadel or the service account.
func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	var ref *refusal
	if errors.As(err, &ref) {
		core.WriteError(w, http.StatusConflict, ref.message)
		return
	}
	var zerr *zitadel.Error
	if errors.As(err, &zerr) {
		switch zerr.Code {
		case zitadel.CodeNotFound:
			core.WriteError(w, http.StatusNotFound, zerr.Message)
			return
		case zitadel.CodeInvalidArgument, zitadel.CodeOutOfRange:
			core.WriteError(w, http.StatusBadRequest, zerr.Message)
			return
		case zitadel.CodeAlreadyExists, zitadel.CodeFailedPrecondition, zitadel.CodeAborted:
			core.WriteError(w, http.StatusConflict, zerr.Message)
			return
		case zitadel.CodeDeadlineExceeded:
			s.Log.WarnContext(ctx, "Zitadel timed out", "err", err)
			core.WriteError(w, http.StatusGatewayTimeout, "Zitadel did not answer in time")
			return
		case zitadel.CodeUnauthenticated, zitadel.CodePermissionDenied:
			s.Log.ErrorContext(ctx, "Zitadel refused the service account", "err", err)
			core.WriteError(w, http.StatusBadGateway, "Zitadel refused the admin app's service account")
			return
		}
	}
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		// The browser went away; nobody reads the answer.
		s.Log.DebugContext(ctx, "request canceled", "err", err)
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout():
		s.Log.WarnContext(ctx, "Zitadel timed out", "err", err)
		core.WriteError(w, http.StatusGatewayTimeout, "Zitadel did not answer in time")
		return
	default:
		s.Log.ErrorContext(ctx, "Zitadel call failed", "err", err)
	}
	core.WriteError(w, http.StatusBadGateway, "Zitadel is unavailable")
}
