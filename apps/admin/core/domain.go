package core

import (
	"net/http"
	"slices"
	"time"
)

// Present only when the project and the app both assert roles in the ID token.
const RolesClaim = "urn:zitadel:iam:org:project:roles"

// Roles are copied at sign-in, so a revoked role takes effect at the next sign-in.
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

type SessionReader interface {
	Session(r *http.Request) (Session, bool)
}
