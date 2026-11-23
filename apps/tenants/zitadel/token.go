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

type Tokens interface {
	Token(ctx context.Context) (string, error)
	Invalidate(token string)
}

const zitadelAPIScope = "urn:zitadel:iam:org:project:id:zitadel:aud"

// Tokens shorter than 4×renewBefore renew at a quarter of life left (the prototype's live 60 s).
const renewBefore = 2 * time.Minute

type tokenSource interface {
	TokenCtx(ctx context.Context) (*oauth2.Token, error)
}

type KeyTokens struct {
	src tokenSource
	now func() time.Time

	mu     sync.Mutex
	tok    *oauth2.Token
	margin time.Duration
}

// ctx is used only during setup; no request is made.
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

func (k *KeyTokens) Token(ctx context.Context) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.tok != nil && k.tok.AccessToken != "" && (k.tok.Expiry.IsZero() || k.now().Add(k.margin).Before(k.tok.Expiry)) {
		return k.tok.AccessToken, nil
	}
	fetched := k.now()
	tok, err := k.src.TokenCtx(ctx)
	if err != nil {
		return "", fmt.Errorf("zitadel: getting an access token: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("zitadel: the token answer has no access token")
	}
	k.tok = tok
	k.margin = min(renewBefore, tok.Expiry.Sub(fetched)/4)
	return tok.AccessToken, nil
}

func (k *KeyTokens) Invalidate(token string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.tok != nil && k.tok.AccessToken == token {
		k.tok = nil
	}
}
