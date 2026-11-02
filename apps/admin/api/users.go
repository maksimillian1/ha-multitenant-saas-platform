package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request, _ core.Session) {
	q := r.URL.Query()
	p, err := page(q)
	if err != nil {
		badRequest(w, err)
		return
	}
	filter := zitadel.UserFilter{Search: strings.TrimSpace(q.Get("search")), OrganizationID: strings.TrimSpace(q.Get("org_id"))}
	if utf8.RuneCountInString(filter.Search) > 200 || len(filter.OrganizationID) > 200 {
		badRequest(w, errors.New("search and org_id have at most 200 characters"))
		return
	}
	switch UserType(q.Get("type")) {
	case "":
	case UserTypeHuman:
		filter.Type = zitadel.TypeHuman
	case UserTypeMachine:
		filter.Type = zitadel.TypeMachine
	default:
		badRequest(w, errors.New("type must be human or machine"))
		return
	}
	switch UserState(q.Get("state")) {
	case "":
	case UserStateActive:
		filter.State = zitadel.UserActive
	case UserStateInactive:
		filter.State = zitadel.UserInactive
	case UserStateLocked:
		filter.State = zitadel.UserLocked
	case UserStateInitial:
		filter.State = zitadel.UserInitial
	default:
		badRequest(w, errors.New("state must be active, inactive, locked or initial"))
		return
	}

	list, err := s.Zitadel.ListUsers(r.Context(), filter, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	orgIDs := make([]string, 0, len(list.Users))
	for _, u := range list.Users {
		orgIDs = append(orgIDs, u.Details.ResourceOwner)
	}
	names := s.orgNames(r.Context(), orgIDs...)
	items := make([]User, 0, len(list.Users))
	for _, u := range list.Users {
		items = append(items, userFromZitadel(u, names))
	}
	core.WriteJSON(w, http.StatusOK, UserList{Items: items, Total: int64(list.Total)})
}

func (s *Service) getUser(w http.ResponseWriter, r *http.Request, _ core.Session) {
	u, err := s.Zitadel.GetUser(r.Context(), r.PathValue("userId"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, userFromZitadel(u, s.orgNames(r.Context(), u.Details.ResourceOwner)))
}

func (s *Service) createHumanUser(w http.ResponseWriter, r *http.Request, _ core.Session) {
	var body HumanUserCreate
	if !decode(w, r, &body) {
		return
	}
	var f fields
	f.text("org_id", &body.OrgId, 1, 200)
	f.optionalText("username", body.Username, 200, true)
	f.text("given_name", &body.GivenName, 1, 200)
	f.text("family_name", &body.FamilyName, 1, 200)
	f.optionalText("display_name", body.DisplayName, 200, true)
	f.email("email", &body.Email)
	if body.Password != nil && *body.Password != "" {
		f.secret("password", *body.Password)
	}
	if err := f.err(); err != nil {
		badRequest(w, err)
		return
	}
	id, err := s.Zitadel.CreateHumanUser(r.Context(), zitadel.NewHumanUser{
		OrganizationID:         body.OrgId,
		Username:               stringValue(body.Username),
		GivenName:              body.GivenName,
		FamilyName:             body.FamilyName,
		DisplayName:            stringValue(body.DisplayName),
		Email:                  body.Email,
		EmailVerified:          boolValue(body.EmailVerified),
		Password:               stringValue(body.Password),
		PasswordChangeRequired: boolValue(body.PasswordChangeRequired),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	created(w, id)
}

func (s *Service) createMachineUser(w http.ResponseWriter, r *http.Request, _ core.Session) {
	var body MachineUserCreate
	if !decode(w, r, &body) {
		return
	}
	var f fields
	f.text("org_id", &body.OrgId, 1, 200)
	f.text("username", &body.Username, 1, 200)
	f.text("name", &body.Name, 1, 200)
	f.optionalText("description", body.Description, 500, true)
	tokenType := zitadel.AccessTokenJWT
	if body.AccessTokenType != nil {
		switch *body.AccessTokenType {
		case AccessTokenTypeJwt:
		case AccessTokenTypeBearer:
			tokenType = zitadel.AccessTokenBearer
		default:
			f.fail("access_token_type must be bearer or jwt")
		}
	}
	if err := f.err(); err != nil {
		badRequest(w, err)
		return
	}
	id, err := s.Zitadel.CreateMachineUser(r.Context(), zitadel.NewMachineUser{
		OrganizationID:  body.OrgId,
		Username:        body.Username,
		Name:            body.Name,
		Description:     stringValue(body.Description),
		AccessTokenType: tokenType,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	created(w, id)
}

func (s *Service) updateUser(w http.ResponseWriter, r *http.Request, _ core.Session) {
	var body UserUpdate
	if !decode(w, r, &body) {
		return
	}
	var f fields
	switch {
	case body.Human != nil && body.Machine != nil:
		f.fail("set human or machine, not both")
	case body.Username == nil && body.Human == nil && body.Machine == nil:
		f.fail("nothing to change")
	}
	f.optionalText("username", body.Username, 200, false)
	update := zitadel.UserUpdate{Username: body.Username}
	if h := body.Human; h != nil {
		f.optionalText("given_name", h.GivenName, 200, false)
		f.optionalText("family_name", h.FamilyName, 200, false)
		f.optionalText("nick_name", h.NickName, 200, false)
		f.optionalText("display_name", h.DisplayName, 200, false)
		if h.Email != nil {
			f.email("email", h.Email)
		}
		update.Human = &zitadel.HumanUpdate{
			GivenName: h.GivenName, FamilyName: h.FamilyName, NickName: h.NickName, DisplayName: h.DisplayName,
			Email: h.Email, EmailVerified: boolValue(h.EmailVerified),
		}
	}
	if m := body.Machine; m != nil {
		f.optionalText("name", m.Name, 200, false)
		f.optionalText("description", m.Description, 500, true)
		update.Machine = &zitadel.MachineUpdate{Name: m.Name, Description: m.Description}
	}
	if err := f.err(); err != nil {
		badRequest(w, err)
		return
	}
	if err := s.Zitadel.UpdateUser(r.Context(), r.PathValue("userId"), update); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}

func (s *Service) setUserPassword(w http.ResponseWriter, r *http.Request, _ core.Session) {
	var body PasswordSet
	if !decode(w, r, &body) {
		return
	}
	var f fields
	f.secret("password", body.Password)
	if err := f.err(); err != nil {
		badRequest(w, err)
		return
	}
	if err := s.Zitadel.SetPassword(r.Context(), r.PathValue("userId"), body.Password, boolValue(body.ChangeRequired)); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}

func (s *Service) deactivateUser(w http.ResponseWriter, r *http.Request, admin core.Session) {
	s.guardedUserAction(w, r, admin, "deactivate", s.Zitadel.DeactivateUser)
}

func (s *Service) lockUser(w http.ResponseWriter, r *http.Request, admin core.Session) {
	s.guardedUserAction(w, r, admin, "lock", s.Zitadel.LockUser)
}

func (s *Service) deleteUser(w http.ResponseWriter, r *http.Request, admin core.Session) {
	s.guardedUserAction(w, r, admin, "delete", s.Zitadel.DeleteUser)
}

func (s *Service) reactivateUser(w http.ResponseWriter, r *http.Request, _ core.Session) {
	s.userAction(w, r, s.Zitadel.ReactivateUser)
}

func (s *Service) unlockUser(w http.ResponseWriter, r *http.Request, _ core.Session) {
	s.userAction(w, r, s.Zitadel.UnlockUser)
}

func (s *Service) removeUserSecret(w http.ResponseWriter, r *http.Request, _ core.Session) {
	s.userAction(w, r, s.Zitadel.RemoveSecret)
}

type userActionFunc func(ctx context.Context, userID string) error

// guardedUserAction runs an action that could lock the admin or the app
// out, after checkUserChange.
func (s *Service) guardedUserAction(w http.ResponseWriter, r *http.Request, admin core.Session, verb string, action userActionFunc) {
	if err := s.checkUserChange(r.Context(), admin, r.PathValue("userId"), verb); err != nil {
		s.fail(w, r, err)
		return
	}
	s.userAction(w, r, action)
}

func (s *Service) userAction(w http.ResponseWriter, r *http.Request, action userActionFunc) {
	if err := action(r.Context(), r.PathValue("userId")); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}

// generateUserSecret replaces a machine user's client secret and returns
// the client credentials; the username is the client ID.
func (s *Service) generateUserSecret(w http.ResponseWriter, r *http.Request, _ core.Session) {
	id := r.PathValue("userId")
	u, err := s.Zitadel.GetUser(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if u.Machine == nil {
		s.fail(w, r, &refusal{"Only machine users have a client secret."})
		return
	}
	secret, err := s.Zitadel.AddSecret(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, MachineSecret{ClientId: u.Username, ClientSecret: secret})
}

func (s *Service) listUserAuthorizations(w http.ResponseWriter, r *http.Request, _ core.Session) {
	list, err := s.Zitadel.ListAuthorizations(r.Context(), zitadel.AuthorizationFilter{UserID: r.PathValue("userId")}, zitadel.Page{Limit: maxRelated})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, authorizationList(list))
}
