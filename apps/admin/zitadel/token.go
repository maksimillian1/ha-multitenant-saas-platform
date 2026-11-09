package zitadel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	jwtprofile "github.com/zitadel/oidc/v3/pkg/client/profile"
	"golang.org/x/oauth2"
)

// Tokens gives the client the access token for its calls. Invalidate drops a
// token that Zitadel refused, so the next Token gets a new one.
type Tokens interface {
	Token(ctx context.Context) (string, error)
	Invalidate(token string)
}

// zitadelAPIScope asks for Zitadel's own API in the token's audience.
const zitadelAPIScope = "urn:zitadel:iam:org:project:id:zitadel:aud"

// renewBefore is how long before its expiry an access token is replaced.
const renewBefore = 2 * time.Minute

// tokenSource is the part of zitadel/oidc's profile.TokenSource we use.
type tokenSource interface {
	TokenCtx(ctx context.Context) (*oauth2.Token, error)
}

// KeyTokens gets access tokens from Zitadel by the JWT bearer grant
// (RFC 7523) with a machine user's JWT profile key, keeps the current one and
// gets the next one shortly before it expires.
type KeyTokens struct {
	src tokenSource
	now func() time.Time

	mu  sync.Mutex
	tok *oauth2.Token
}

// NewKeyTokens returns the tokens of the machine user of key at the Zitadel
// instance whose issuer is issuer. httpClient carries the token requests;
// ctx is used only while the source is set up (no request is made).
func NewKeyTokens(ctx context.Context, issuer string, key Key, httpClient *http.Client) (*KeyTokens, error) {
	issuer = strings.TrimSuffix(issuer, "/")
	src, err := jwtprofile.NewJWTProfileTokenSource(ctx, issuer, key.UserID, key.KeyID, []byte(key.Key),
		[]string{"openid", zitadelAPIScope},
		jwtprofile.WithHTTPClient(httpClient),
		// A fixed endpoint: no discovery call at start.
		jwtprofile.WithStaticTokenEndpoint(issuer, issuer+"/oauth/v2/token"))
	if err != nil {
		return nil, fmt.Errorf("zitadel: the key %s of user %s: %w", key.KeyID, key.UserID, err)
	}
	return &KeyTokens{src: src, now: time.Now}, nil
}

// Token returns the current access token, getting a new one when there is
// none or when it expires within renewBefore.
func (k *KeyTokens) Token(ctx context.Context) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.tok != nil && k.tok.AccessToken != "" && (k.tok.Expiry.IsZero() || k.now().Add(renewBefore).Before(k.tok.Expiry)) {
		return k.tok.AccessToken, nil
	}
	tok, err := k.src.TokenCtx(ctx)
	if err != nil {
		return "", fmt.Errorf("zitadel: getting an access token: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("zitadel: the token answer has no access token")
	}
	k.tok = tok
	return tok.AccessToken, nil
}

// Invalidate drops token if it is still the current one.
func (k *KeyTokens) Invalidate(token string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.tok != nil && k.tok.AccessToken == token {
		k.tok = nil
	}
}
