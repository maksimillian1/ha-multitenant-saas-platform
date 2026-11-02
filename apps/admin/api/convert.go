package api

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

// maxLookups is how many organizations orgNames reads at the same time.
const maxLookups = 8

// orgNames returns the names of the organizations, read in parallel. An
// organization that cannot be read has no entry: its name shows as empty
// instead of failing the whole page.
func (s *Service) orgNames(ctx context.Context, ids ...string) map[string]string {
	unique := slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id == "" })
	slices.Sort(unique)
	unique = slices.Compact(unique)
	names := make(map[string]string, len(unique))
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		slot = make(chan struct{}, maxLookups)
	)
	for _, id := range unique {
		wg.Go(func() {
			slot <- struct{}{}
			defer func() { <-slot }()
			org, err := s.Zitadel.GetOrganization(ctx, id)
			if err != nil {
				s.Log.WarnContext(ctx, "organization name not read", "org", id, "err", err)
				return
			}
			mu.Lock()
			names[id] = org.Name
			mu.Unlock()
		})
	}
	wg.Wait()
	return names
}

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func stringOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func orgFromZitadel(o zitadel.Organization, defaultID string) Org {
	return Org{
		Id:            o.ID,
		Name:          o.Name,
		State:         orgState(o.State),
		PrimaryDomain: o.PrimaryDomain,
		IsDefault:     defaultID != "" && o.ID == defaultID,
		CreatedAt:     timeOrNil(o.Details.CreationDate),
		ChangedAt:     timeOrNil(o.Details.ChangeDate),
	}
}

func orgState(s zitadel.OrganizationState) OrgState {
	switch s {
	case zitadel.OrganizationActive:
		return OrgStateActive
	case zitadel.OrganizationInactive:
		return OrgStateInactive
	case zitadel.OrganizationRemoved:
		return OrgStateRemoved
	default:
		return OrgStateUnspecified
	}
}

func userFromZitadel(u zitadel.User, orgNames map[string]string) User {
	out := User{
		Id:                 u.UserID,
		Type:               UserTypeHuman,
		State:              userState(u.State),
		Username:           u.Username,
		DisplayName:        u.Username,
		LoginNames:         nonNil(u.LoginNames),
		PreferredLoginName: u.PreferredLoginName,
		OrgId:              u.Details.ResourceOwner,
		OrgName:            orgNames[u.Details.ResourceOwner],
		CreatedAt:          timeOrNil(u.Details.CreationDate),
		ChangedAt:          timeOrNil(u.Details.ChangeDate),
	}
	switch {
	case u.Human != nil:
		h := u.Human
		out.Human = &HumanProfile{
			GivenName:              h.Profile.GivenName,
			FamilyName:             h.Profile.FamilyName,
			NickName:               h.Profile.NickName,
			DisplayName:            h.Profile.DisplayName,
			Email:                  h.Email.Email,
			EmailVerified:          h.Email.IsVerified,
			Phone:                  h.Phone.Phone,
			PhoneVerified:          h.Phone.IsVerified,
			PasswordChangeRequired: h.PasswordChangeRequired,
			PasswordChangedAt:      timeOrNil(h.PasswordChanged),
		}
		out.DisplayName = firstNonEmpty(h.Profile.DisplayName, strings.TrimSpace(h.Profile.GivenName+" "+h.Profile.FamilyName), u.Username)
	case u.Machine != nil:
		m := u.Machine
		out.Type = UserTypeMachine
		out.Machine = &MachineProfile{
			Name:            m.Name,
			Description:     m.Description,
			HasSecret:       m.HasSecret,
			AccessTokenType: accessTokenType(m.AccessTokenType),
		}
		out.DisplayName = firstNonEmpty(m.Name, u.Username)
	}
	return out
}

func userState(s zitadel.UserState) UserState {
	switch s {
	case zitadel.UserActive:
		return UserStateActive
	case zitadel.UserInactive:
		return UserStateInactive
	case zitadel.UserLocked:
		return UserStateLocked
	case zitadel.UserInitial:
		return UserStateInitial
	case zitadel.UserDeleted:
		return UserStateDeleted
	default:
		return UserStateUnspecified
	}
}

// accessTokenType maps Zitadel's enum; protojson leaves out the default,
// bearer.
func accessTokenType(t zitadel.AccessTokenType) AccessTokenType {
	if t == zitadel.AccessTokenJWT {
		return AccessTokenTypeJwt
	}
	return AccessTokenTypeBearer
}

func authorizationFromZitadel(a zitadel.Authorization) Authorization {
	roles := make([]Role, 0, len(a.Roles))
	for _, r := range a.Roles {
		roles = append(roles, Role{Key: r.Key, DisplayName: r.DisplayName, Group: r.Group})
	}
	state := AuthorizationStateUnspecified
	switch a.State {
	case zitadel.AuthorizationActive:
		state = AuthorizationStateActive
	case zitadel.AuthorizationInactive:
		state = AuthorizationStateInactive
	}
	return Authorization{
		Id:              a.ID,
		State:           state,
		ProjectId:       a.Project.ID,
		ProjectName:     a.Project.Name,
		ProjectOrgId:    a.Project.OrganizationID,
		OrgId:           a.Organization.ID,
		OrgName:         a.Organization.Name,
		UserId:          a.User.ID,
		UserLoginName:   a.User.PreferredLoginName,
		UserDisplayName: a.User.DisplayName,
		UserOrgId:       a.User.OrganizationID,
		Roles:           roles,
		CreatedAt:       timeOrNil(a.CreationDate),
		ChangedAt:       timeOrNil(a.ChangeDate),
	}
}

func authorizationList(list zitadel.AuthorizationList) AuthorizationList {
	items := make([]Authorization, 0, len(list.Authorizations))
	for _, a := range list.Authorizations {
		items = append(items, authorizationFromZitadel(a))
	}
	return AuthorizationList{Items: items, Total: int64(list.Total)}
}

func projectFromZitadel(p zitadel.Project, orgNames map[string]string) Project {
	state := ProjectStateUnspecified
	switch p.State {
	case zitadel.ProjectActive:
		state = ProjectStateActive
	case zitadel.ProjectInactive:
		state = ProjectStateInactive
	}
	return Project{
		Id:                    p.ID,
		Name:                  p.Name,
		OrgId:                 p.OrganizationID,
		OrgName:               orgNames[p.OrganizationID],
		State:                 state,
		RoleAssertion:         p.ProjectRoleAssertion,
		AuthorizationRequired: p.AuthorizationRequired,
		AccessRequired:        p.ProjectAccessRequired,
		GrantedOrgId:          stringOrNil(p.GrantedOrganizationID),
		GrantedOrgName:        stringOrNil(p.GrantedOrganizationName),
		CreatedAt:             timeOrNil(p.CreationDate),
		ChangedAt:             timeOrNil(p.ChangeDate),
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// nonNil makes an absent list an empty JSON array instead of null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
