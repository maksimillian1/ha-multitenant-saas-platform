package auth

import "golang.org/x/oauth2"

func oidcToken(access, refresh string) *oauth2.Token {
	return &oauth2.Token{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer"}
}
