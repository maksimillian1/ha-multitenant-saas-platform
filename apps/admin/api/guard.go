package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

// checkCSRF protects state-changing requests against cross-site request
// forgery (the risk ADR-0009 lists for the first mutation). The session
// cookie is SameSite=Lax, and on top of that:
//
//   - the request must carry X-Requested-With, which a cross-site form cannot
//     send and a cross-site fetch cannot send without a CORS preflight, which
//     this server never answers;
//   - Go's CrossOriginProtection rejects requests that the browser marks as
//     cross-site (Sec-Fetch-Site) or whose Origin is neither the request's
//     host nor PUBLIC_URL.
func (s *Service) checkCSRF(r *http.Request) error {
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		return errors.New("no X-Requested-With: XMLHttpRequest header")
	}
	return s.csrf.Check(r)
}

// refusal is a change the API does not make because it would lock the admin
// or the app itself out; it is answered with 409.
type refusal struct{ message string }

func (r *refusal) Error() string { return r.message }

// checkUserChange refuses to lock, deactivate or delete the signed-in admin
// or the app's own service account.
func (s *Service) checkUserChange(ctx context.Context, admin core.Session, userID, verb string) error {
	if userID == admin.Subject {
		return &refusal{"You cannot " + verb + " your own user."}
	}
	account, err := s.Zitadel.ServiceAccount(ctx)
	if err != nil {
		return err
	}
	if userID == account.UserID {
		return &refusal{"You cannot " + verb + " the admin app's service account."}
	}
	return nil
}

// checkOrgChange refuses to deactivate the organization of the
// signed-in admin or of the app's service account.
func (s *Service) checkOrgChange(ctx context.Context, admin core.Session, orgID, verb string) error {
	me, err := s.Zitadel.GetUser(ctx, admin.Subject)
	switch {
	case err == nil && me.Details.ResourceOwner == orgID:
		return &refusal{"You cannot " + verb + " your own organization."}
	case err != nil && !zitadel.HasCode(err, zitadel.CodeNotFound):
		return err
	}
	account, err := s.Zitadel.ServiceAccount(ctx)
	if err != nil {
		return err
	}
	if account.OrganizationID == orgID {
		return &refusal{"You cannot " + verb + " the organization of the admin app's service account."}
	}
	return nil
}

// checkAuthorizationChange refuses to change or remove the signed-in admin's
// own role assignments, which could take away their admin role.
func (s *Service) checkAuthorizationChange(ctx context.Context, admin core.Session, id string) error {
	a, err := s.Zitadel.GetAuthorization(ctx, id)
	if err != nil {
		return err
	}
	if a.User.ID == admin.Subject {
		return &refusal{"You cannot change your own role assignments."}
	}
	return nil
}
