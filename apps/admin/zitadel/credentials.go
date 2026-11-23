package zitadel

import (
	"context"
	"time"
)

const (
	CredentialKey                 = "key"
	CredentialPersonalAccessToken = "personal_access_token"
)

// Zitadel never returns the secret after creation.
type Credential struct {
	Kind    string
	ID      string
	Expires time.Time // zero: never
}

// A machine user of ours has one key.
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

// "" when there is none.
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
