package core

import (
	"net/http"
	"slices"
	"time"
)

// RolesClaim lists the user's roles in the project of the OIDC app, as
// {"role": {"orgID": "org domain"}}. Zitadel adds it to the ID token when the
// project asserts roles and the app asserts them in the ID token.
const RolesClaim = "urn:zitadel:iam:org:project:roles"

// Session is the signed-in user, kept in an encrypted cookie. Roles are
// copied at sign-in, so a revoked role takes effect at the next sign-in.
type Session struct {
	Subject string    `json:"sub"`
	Name    string    `json:"name"`
	Email   string    `json:"email,omitempty"`
	Roles   []string  `json:"roles"`
	Expires time.Time `json:"exp"`
}

func (s Session) HasRole(role string) bool {
	return slices.Contains(s.Roles, role)
}

// SessionReader returns the session of a request, if it has a valid one.
type SessionReader interface {
	Session(r *http.Request) (Session, bool)
}
