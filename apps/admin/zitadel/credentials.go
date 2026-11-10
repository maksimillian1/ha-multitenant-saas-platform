package zitadel

import (
	"context"
	"time"
)

// Credential kinds of a machine user.
const (
	CredentialKey                 = "key"
	CredentialPersonalAccessToken = "personal_access_token"
)

// Credential is a key or a personal access token of a machine user, without
// its secret, which Zitadel never returns after creation.
type Credential struct {
	Kind    string
	ID      string
	Expires time.Time // zero: never
}

// maxCredentials bounds one listing; a machine user of ours has one key.
const maxCredentials = 100

type userIDFilter struct {
	UserIDFilter idFilter `json:"userIdFilter"`
}

type credentialList struct {
	Result []struct {
		ID             string     `json:"id"`
		ExpirationDate *time.Time `json:"expirationDate"`
	} `json:"result"`
}

// ListCredentials returns the keys and the personal access tokens of a
// machine user (user.v2 ListKeys and ListPersonalAccessTokens).
func (c *Client) ListCredentials(ctx context.Context, userID string) ([]Credential, error) {
	req := struct {
		Pagination *pagination    `json:"pagination"`
		Filters    []userIDFilter `json:"filters"`
	}{
		Pagination: Page{Limit: maxCredentials}.ascending(),
		Filters:    []userIDFilter{{UserIDFilter: idFilter{ID: userID}}},
	}
	var out []Credential
	for _, list := range []struct{ method, kind string }{
		{"ListKeys", CredentialKey},
		{"ListPersonalAccessTokens", CredentialPersonalAccessToken},
	} {
		var resp credentialList
		if err := c.call(ctx, userService, list.method, req, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Result {
			cred := Credential{Kind: list.kind, ID: r.ID}
			if r.ExpirationDate != nil {
				cred.Expires = *r.ExpirationDate
			}
			out = append(out, cred)
		}
	}
	return out, nil
}

// UserIDByName returns the ID of the user with exactly this user name in any
// organization, or "" when there is none.
func (c *Client) UserIDByName(ctx context.Context, userName string) (string, error) {
	req := struct {
		Query   *pagination `json:"query"`
		Queries []userQuery `json:"queries"`
	}{
		Query:   Page{Limit: 2}.ascending(),
		Queries: []userQuery{{UserNameQuery: &userText{UserName: userName, Method: textEquals}}},
	}
	var resp struct {
		Result []struct {
			UserID string `json:"userId"`
		} `json:"result"`
	}
	if err := c.call(ctx, userService, "ListUsers", req, &resp); err != nil {
		return "", err
	}
	if len(resp.Result) == 0 {
		return "", nil
	}
	return resp.Result[0].UserID, nil
}
