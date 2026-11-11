package zitadel

import (
	"context"
	"time"
)

// User is a user.v2 User: a human or a machine (service account).
// Details.ResourceOwner is the user's organization.
type User struct {
	UserID             string       `json:"userId"`
	Details            Details      `json:"details"`
	State              UserState    `json:"state"`
	Username           string       `json:"username"`
	LoginNames         []string     `json:"loginNames"`
	PreferredLoginName string       `json:"preferredLoginName"`
	Human              *HumanUser   `json:"human"`
	Machine            *MachineUser `json:"machine"`
}

// HumanUser holds the fields of user.v2.HumanUser that the admin app shows.
type HumanUser struct {
	Profile struct {
		GivenName   string `json:"givenName"`
		FamilyName  string `json:"familyName"`
		NickName    string `json:"nickName"`
		DisplayName string `json:"displayName"`
	} `json:"profile"`
	Email struct {
		Email      string `json:"email"`
		IsVerified bool   `json:"isVerified"`
	} `json:"email"`
	Phone struct {
		Phone      string `json:"phone"`
		IsVerified bool   `json:"isVerified"`
	} `json:"phone"`
	PasswordChangeRequired bool      `json:"passwordChangeRequired"`
	PasswordChanged        time.Time `json:"passwordChanged"`
}

// MachineUser is user.v2.MachineUser.
type MachineUser struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	HasSecret       bool            `json:"hasSecret"`
	AccessTokenType AccessTokenType `json:"accessTokenType"`
}

// UserState is user.v2.UserState.
type UserState string

const (
	UserActive   UserState = "USER_STATE_ACTIVE"
	UserInactive UserState = "USER_STATE_INACTIVE"
	UserDeleted  UserState = "USER_STATE_DELETED"
	UserLocked   UserState = "USER_STATE_LOCKED"
	UserInitial  UserState = "USER_STATE_INITIAL"
)

// UserType is user.v2.Type, used to filter users.
type UserType string

const (
	TypeHuman   UserType = "TYPE_HUMAN"
	TypeMachine UserType = "TYPE_MACHINE"
)

// AccessTokenType is user.v2.AccessTokenType. protojson leaves out the
// default, so an empty value means bearer.
type AccessTokenType string

const (
	AccessTokenBearer AccessTokenType = "ACCESS_TOKEN_TYPE_BEARER"
	AccessTokenJWT    AccessTokenType = "ACCESS_TOKEN_TYPE_JWT"
)

// UserFilter narrows ListUsers. Zero fields match every user; set fields
// must all match.
type UserFilter struct {
	Search         string // part of the username, display name or email, in any case
	OrganizationID string
	Type           UserType
	State          UserState
}

type UserList struct {
	Total uint64
	Users []User
}

// userQuery is user.v2.SearchQuery, a oneof: exactly one field is set.
type userQuery struct {
	OrQuery             *orQuery     `json:"orQuery,omitempty"`
	UserNameQuery       *userText    `json:"userNameQuery,omitempty"`
	DisplayNameQuery    *displayText `json:"displayNameQuery,omitempty"`
	EmailQuery          *emailText   `json:"emailQuery,omitempty"`
	OrganizationIDQuery *struct {
		OrganizationID string `json:"organizationId"`
	} `json:"organizationIdQuery,omitempty"`
	TypeQuery *struct {
		Type UserType `json:"type"`
	} `json:"typeQuery,omitempty"`
	StateQuery *struct {
		State UserState `json:"state"`
	} `json:"stateQuery,omitempty"`
}

type orQuery struct {
	Queries []userQuery `json:"queries"`
}

type userText struct {
	UserName string `json:"userName"`
	Method   string `json:"method"`
}

type displayText struct {
	DisplayName string `json:"displayName"`
	Method      string `json:"method"`
}

type emailText struct {
	EmailAddress string `json:"emailAddress"`
	Method       string `json:"method"`
}

// ListUsers returns the users of every organization the service account may
// read, sorted by username.
func (c *Client) ListUsers(ctx context.Context, f UserFilter, p Page) (UserList, error) {
	req := struct {
		Query         *pagination `json:"query"`
		SortingColumn string      `json:"sortingColumn"`
		Queries       []userQuery `json:"queries,omitempty"`
	}{Query: p.ascending(), SortingColumn: "USER_FIELD_NAME_USER_NAME"}
	if f.Search != "" {
		req.Queries = append(req.Queries, userQuery{OrQuery: &orQuery{Queries: []userQuery{
			{UserNameQuery: &userText{UserName: f.Search, Method: textContainsIgnoreCase}},
			{DisplayNameQuery: &displayText{DisplayName: f.Search, Method: textContainsIgnoreCase}},
			{EmailQuery: &emailText{EmailAddress: f.Search, Method: textContainsIgnoreCase}},
		}}})
	}
	if f.OrganizationID != "" {
		q := userQuery{OrganizationIDQuery: &struct {
			OrganizationID string `json:"organizationId"`
		}{OrganizationID: f.OrganizationID}}
		req.Queries = append(req.Queries, q)
	}
	if f.Type != "" {
		req.Queries = append(req.Queries, userQuery{TypeQuery: &struct {
			Type UserType `json:"type"`
		}{Type: f.Type}})
	}
	if f.State != "" {
		req.Queries = append(req.Queries, userQuery{StateQuery: &struct {
			State UserState `json:"state"`
		}{State: f.State}})
	}

	var resp struct {
		Details struct {
			TotalResult Uint64 `json:"totalResult"`
		} `json:"details"`
		Result []User `json:"result"`
	}
	if err := c.call(ctx, userService, "ListUsers", req, &resp); err != nil {
		return UserList{}, err
	}
	return UserList{Total: uint64(resp.Details.TotalResult), Users: resp.Result}, nil
}

func (c *Client) GetUser(ctx context.Context, id string) (User, error) {
	var resp struct {
		User User `json:"user"`
	}
	if err := c.call(ctx, userService, "GetUserByID", userRequest{UserID: id}, &resp); err != nil {
		return User{}, err
	}
	if resp.User.UserID == "" {
		return User{}, notFound("user")
	}
	return resp.User, nil
}

// NewHumanUser is a person who signs in. Without a password the user stays
// in the initial state until they set one. Without EmailVerified Zitadel
// sends a verification code, if it can send email.
type NewHumanUser struct {
	OrganizationID         string
	Username               string // optional: Zitadel uses the email
	GivenName              string
	FamilyName             string
	DisplayName            string // optional
	Email                  string
	EmailVerified          bool
	Password               string // optional
	PasswordChangeRequired bool
}

// NewMachineUser is a service account, such as a tenant's API client.
type NewMachineUser struct {
	OrganizationID  string
	Username        string
	Name            string
	Description     string // optional
	AccessTokenType AccessTokenType
}

type profile struct {
	GivenName   *string `json:"givenName,omitempty"`
	FamilyName  *string `json:"familyName,omitempty"`
	NickName    *string `json:"nickName,omitempty"`
	DisplayName *string `json:"displayName,omitempty"`
}

// email is user.v2.SetHumanEmail. Without IsVerified the verification oneof
// is unset, and Zitadel sends a code.
type email struct {
	Email      string `json:"email"`
	IsVerified *bool  `json:"isVerified,omitempty"`
}

func newEmail(address string, verified bool) *email {
	e := &email{Email: address}
	if verified {
		e.IsVerified = &verified
	}
	return e
}

type password struct {
	Password       string `json:"password"`
	ChangeRequired bool   `json:"changeRequired,omitempty"`
}

type machine struct {
	Name            *string         `json:"name,omitempty"`
	Description     *string         `json:"description,omitempty"`
	AccessTokenType AccessTokenType `json:"accessTokenType,omitempty"`
}

type createUserRequest struct {
	OrganizationID string `json:"organizationId"`
	Username       string `json:"username,omitempty"`
	Human          *struct {
		Profile  profile   `json:"profile"`
		Email    *email    `json:"email"`
		Password *password `json:"password,omitempty"`
	} `json:"human,omitempty"`
	Machine *machine `json:"machine,omitempty"`
}

// CreateHumanUser creates the user with user.v2 CreateUser and returns its ID.
func (c *Client) CreateHumanUser(ctx context.Context, u NewHumanUser) (string, error) {
	req := createUserRequest{OrganizationID: u.OrganizationID, Username: u.Username}
	req.Human = &struct {
		Profile  profile   `json:"profile"`
		Email    *email    `json:"email"`
		Password *password `json:"password,omitempty"`
	}{
		Profile: profile{GivenName: &u.GivenName, FamilyName: &u.FamilyName, DisplayName: optional(u.DisplayName)},
		Email:   newEmail(u.Email, u.EmailVerified),
	}
	if u.Password != "" {
		req.Human.Password = &password{Password: u.Password, ChangeRequired: u.PasswordChangeRequired}
	}
	return c.createUser(ctx, req)
}

// CreateMachineUser creates the service account with user.v2 CreateUser and
// returns its ID.
func (c *Client) CreateMachineUser(ctx context.Context, u NewMachineUser) (string, error) {
	req := createUserRequest{OrganizationID: u.OrganizationID, Username: u.Username, Machine: &machine{
		Name: &u.Name, Description: optional(u.Description), AccessTokenType: u.AccessTokenType,
	}}
	return c.createUser(ctx, req)
}

func (c *Client) createUser(ctx context.Context, req createUserRequest) (string, error) {
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, userService, "CreateUser", req, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// UserUpdate changes the set fields; nil fields stay as they are. Human
// fields apply to humans only, machine fields to machines only.
type UserUpdate struct {
	Username *string
	Human    *HumanUpdate
	Machine  *MachineUpdate
}

type HumanUpdate struct {
	GivenName     *string
	FamilyName    *string
	NickName      *string
	DisplayName   *string
	Email         *string
	EmailVerified bool // with Email: mark the new address verified
}

type MachineUpdate struct {
	Name        *string
	Description *string
}

type humanUpdate struct {
	Profile  *profile `json:"profile,omitempty"`
	Email    *email   `json:"email,omitempty"`
	Password *struct {
		Password password `json:"password"`
	} `json:"password,omitempty"`
}

type updateUserRequest struct {
	UserID   string       `json:"userId"`
	Username *string      `json:"username,omitempty"`
	Human    *humanUpdate `json:"human,omitempty"`
	Machine  *machine     `json:"machine,omitempty"`
}

// UpdateUser changes a user with user.v2 UpdateUser.
func (c *Client) UpdateUser(ctx context.Context, id string, u UserUpdate) error {
	req := updateUserRequest{UserID: id, Username: u.Username}
	if h := u.Human; h != nil {
		req.Human = &humanUpdate{}
		if h.GivenName != nil || h.FamilyName != nil || h.NickName != nil || h.DisplayName != nil {
			req.Human.Profile = &profile{GivenName: h.GivenName, FamilyName: h.FamilyName, NickName: h.NickName, DisplayName: h.DisplayName}
		}
		if h.Email != nil {
			req.Human.Email = newEmail(*h.Email, h.EmailVerified)
		}
	}
	if m := u.Machine; m != nil {
		req.Machine = &machine{Name: m.Name, Description: m.Description}
	}
	return c.call(ctx, userService, "UpdateUser", req, nil)
}

// SetPassword sets a human user's password without the current one, as an
// administrator may (UpdateUser; the SetPassword method is deprecated).
func (c *Client) SetPassword(ctx context.Context, id, newPassword string, changeRequired bool) error {
	req := updateUserRequest{UserID: id, Human: &humanUpdate{Password: &struct {
		Password password `json:"password"`
	}{Password: password{Password: newPassword, ChangeRequired: changeRequired}}}}
	return c.call(ctx, userService, "UpdateUser", req, nil)
}

func (c *Client) DeactivateUser(ctx context.Context, id string) error {
	return c.call(ctx, userService, "DeactivateUser", userRequest{UserID: id}, nil)
}

func (c *Client) ReactivateUser(ctx context.Context, id string) error {
	return c.call(ctx, userService, "ReactivateUser", userRequest{UserID: id}, nil)
}

func (c *Client) LockUser(ctx context.Context, id string) error {
	return c.call(ctx, userService, "LockUser", userRequest{UserID: id}, nil)
}

func (c *Client) UnlockUser(ctx context.Context, id string) error {
	return c.call(ctx, userService, "UnlockUser", userRequest{UserID: id}, nil)
}

func (c *Client) DeleteUser(ctx context.Context, id string) error {
	return c.call(ctx, userService, "DeleteUser", userRequest{UserID: id}, nil)
}

// AddSecret generates a new client secret for a machine user, replacing the
// old one, and returns it. Zitadel cannot show it again.
func (c *Client) AddSecret(ctx context.Context, id string) (string, error) {
	var resp struct {
		ClientSecret string `json:"clientSecret"`
	}
	if err := c.call(ctx, userService, "AddSecret", userRequest{UserID: id}, &resp); err != nil {
		return "", err
	}
	return resp.ClientSecret, nil
}

func (c *Client) RemoveSecret(ctx context.Context, id string) error {
	return c.call(ctx, userService, "RemoveSecret", userRequest{UserID: id}, nil)
}

type userRequest struct {
	UserID string `json:"userId"`
}

// optional is nil for "", so protojson leaves an optional field unset.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
