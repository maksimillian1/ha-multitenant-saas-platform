package core

import "time"

const (
	// The organization of the user; the API Gateway takes the tenant from the same claim.
	OrgClaim = "urn:zitadel:iam:user:resourceowner:id"
)

// Session is what exampleweb keeps on the server for a signed-in browser; the
// browser holds only the session's random ID.
type Session struct {
	Subject       string
	Name          string
	OrgID         string
	AccessToken   string
	RefreshToken  string
	IDToken       string
	AccessIssued  time.Time
	AccessExpires time.Time
	Expires       time.Time
}
