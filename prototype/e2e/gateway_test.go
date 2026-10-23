package e2e

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// signRS256 builds a JWT signed with a key the gateway has never seen.
func signRS256(t *testing.T, claims map[string]any) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	signingInput := enc(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "e2e-foreign-key"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// exampleAppLogs returns the access logs of all ExampleApp pods.
func exampleAppLogs(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := kubectl(ctx, "-n", "exampleapp", "logs", "-l", "app.kubernetes.io/name=exampleapp",
		"--tail=-1", "--prefix", "--max-log-requests=10")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// AC-5: requests without a valid token get 401 at the gateway and never reach ExampleApp.
func TestGatewayRejectsInvalidTokens(t *testing.T) {
	valid := token(t, tenA)
	h, _, s, claims := jwtParts(t, valid)

	// Same signature, payload changed to tenant B: the signature no longer matches.
	claims[tenantClaim] = tenB.OrgID
	raw, _ := json.Marshal(claims)
	tampered := h + "." + base64.RawURLEncoding.EncodeToString(raw) + "." + s

	now := time.Now().Unix()
	foreignClaims := func(iss string) map[string]any {
		return map[string]any{"iss": iss, "sub": "e2e", "iat": now, "exp": now + 300, tenantClaim: tenA.OrgID}
	}

	cases := []struct {
		name   string
		bearer string
	}{
		{"no token", ""},
		{"garbage token", "not-a-jwt"},
		{"bad signature", tampered},
		{"forged with Zitadel issuer", signRS256(t, foreignClaims(cfg.Issuer))},
		{"other issuer", signRS256(t, foreignClaims("http://evil.example"))},
	}

	probes := map[string]string{}
	for _, c := range cases {
		probe := newUUID()
		probes[c.name] = probe
		r := call(t, http.MethodGet, "/items/"+probe, c.bearer, nil)
		if r.Status != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401; body %s", c.name, r.Status, r.Body)
		}
	}

	// Positive control: a request with a valid token is logged by ExampleApp.
	control := newUUID()
	mustStatus(t, call(t, http.MethodGet, "/items/"+control, valid, nil), http.StatusNotFound, "valid token, unknown item")

	logs := exampleAppLogs(t)
	if !strings.Contains(logs, control) {
		t.Fatalf("ExampleApp logs miss the control request %s; the log check would prove nothing", control)
	}
	for name, probe := range probes {
		if strings.Contains(logs, probe) {
			t.Errorf("%s: the rejected request reached ExampleApp (path %s is in its logs)", name, probe)
		}
	}
}

// AC-5: an expired Zitadel token is rejected. Takes about a minute, because
// bootstrap-tenants.sh sets the access token lifetime to 60 s.
func TestGatewayRejectsExpiredToken(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for a token to expire")
	}
	tok := token(t, tenA)
	_, _, _, claims := jwtParts(t, tok)
	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatalf("token has no exp: %v", claims)
	}
	mustStatus(t, call(t, http.MethodGet, "/items", tok, nil), http.StatusOK, "fresh token")

	// The gateway allows 5 s of clock skew.
	wait := time.Until(time.Unix(int64(exp), 0)) + 10*time.Second
	if wait > 3*time.Minute {
		t.Fatalf("token lives %s; run bootstrap-tenants.sh to set the 60 s lifetime", wait)
	}
	time.Sleep(wait)

	probe := newUUID()
	r := call(t, http.MethodGet, "/items/"+probe, tok, nil)
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("expired token: status %d, want 401; body %s", r.Status, r.Body)
	}
	if strings.Contains(exampleAppLogs(t), probe) {
		t.Fatal("the request with an expired token reached ExampleApp")
	}
}

// AC-6: pods other than the gateway cannot connect to ExampleApp directly.
func TestDirectAccessToExampleAppIsBlocked(t *testing.T) {
	for _, ns := range []string{"default", "exampleapp"} {
		t.Run(ns, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			// One pod, two requests: the gateway answers (the pod has network),
			// ExampleApp must not.
			script := `
code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' -H 'Host: api.localtest.me' http://api-gateway.gateway.svc/items)
echo "gateway=$code"
if curl -s -m 5 -o /dev/null http://exampleapp.exampleapp.svc:8080/healthz; then echo direct=open; else echo direct=blocked; fi`
			out, err := kubectl(ctx, "-n", ns, "run", "e2e-direct-"+randomID(), "--rm", "-i", "--restart=Never",
				"--quiet", "--image=curlimages/curl:8.11.1", "--command", "--", "sh", "-c", script)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "gateway=401") {
				t.Fatalf("control request to the gateway failed, so the check proves nothing: %q", out)
			}
			if !strings.Contains(out, "direct=blocked") {
				t.Fatalf("pod in %s reached ExampleApp directly: %q", ns, out)
			}
		})
	}
}
