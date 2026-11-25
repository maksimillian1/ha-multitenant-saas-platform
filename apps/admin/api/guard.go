package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

// SameSite=Lax plus X-Requested-With (no cross-site form can send it, and we never answer a CORS
// preflight) plus Go's CrossOriginProtection on Sec-Fetch-Site and Origin (ADR-0009).
func (s *Service) checkCSRF(r *http.Request) error {
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		return errors.New("no X-Requested-With: XMLHttpRequest header")
	}
	return s.csrf.Check(r)
}

// Answered with 409.
type refusal struct{ message string }

func (r *refusal) Error() string { return r.message }

// Refuses to lock out the signed-in admin or the app's own service account.
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

// Changing their own role assignments could take away the admin's role.
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
