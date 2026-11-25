package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	adminWebURL   = "http://admin.localtest.me:30080"
	outageFor     = 2 * time.Minute
	jwksConfigMap = "zitadel-jwks"
)

func kube(t *testing.T, within time.Duration, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	out, err := retryKubectl(ctx, args...)
	if err != nil {
		t.Fatalf("kubectl %v: %v: %s", args, err, out)
	}
	return out
}

const outagePolicy = `apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: e2e-zitadel-outage
  namespace: zitadel
spec:
  endpointSelector:
    matchLabels:
      app.kubernetes.io/instance: zitadel
  ingress:
    - fromEntities: [host]
`

func zitadelDown(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if out, err := kubectlStdin(ctx, outagePolicy, "apply", "-f", "-"); err != nil {
		t.Fatalf("outage policy: %v: %s", err, out)
	}
	for deadline := time.Now().Add(time.Minute); zitadelAnswers(); {
		if time.Now().After(deadline) {
			t.Fatal("Zitadel still answers a minute after the outage policy, so the test proves nothing")
		}
		time.Sleep(2 * time.Second)
	}
}

func zitadelUp(t *testing.T) {
	t.Helper()
	kube(t, time.Minute, "-n", "zitadel", "delete", "ciliumnetworkpolicy", "e2e-zitadel-outage", "--ignore-not-found")
	for deadline := time.Now().Add(5 * time.Minute); !zitadelAnswers(); {
		if time.Now().After(deadline) {
			t.Fatal("Zitadel does not answer 5 minutes after the outage ended")
		}
		time.Sleep(5 * time.Second)
	}
}

func zitadelAnswers() bool {
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(cfg.Issuer + "/.well-known/openid-configuration")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func runJWKSSync(t *testing.T) {
	t.Helper()
	name := "e2e-jwks-sync-" + randomID()
	kube(t, time.Minute, "-n", "gateway", "create", "job", name, "--from=cronjob/jwks-sync")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = kubectl(ctx, "-n", "gateway", "delete", "job", name, "--wait=false")
	})
	kube(t, 2*time.Minute, "-n", "gateway", "wait", "--for=condition=complete", "job/"+name, "--timeout=110s")
}

func cachedKeyIDs(t *testing.T) []string {
	t.Helper()
	raw := kube(t, time.Minute, "-n", "gateway", "get", "configmap", jwksConfigMap, "-o", `jsonpath={.data.jwks\.json}`)
	var c struct {
		Keys []struct {
			Kid string `json:"kid"`
			D   string `json:"d"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("ConfigMap %s: %v: %.200s", jwksConfigMap, err, raw)
	}
	var ids []string
	for _, k := range c.Keys {
		if k.D != "" {
			t.Errorf("the cache holds the private part of key %s", k.Kid)
		}
		ids = append(ids, k.Kid)
	}
	slices.Sort(ids)
	return ids
}

func zitadelKeyIDs(t *testing.T) []string {
	t.Helper()
	resp, err := http.Get(cfg.Issuer + "/oauth/v2/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, k := range set.Keys {
		ids = append(ids, k.Kid)
	}
	slices.Sort(ids)
	return ids
}

func ownItemsOnly(t *testing.T, name, tok, own, other string) {
	t.Helper()
	r := call(t, http.MethodGet, "/items", tok, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("%s: GET /items status %d, want 200; body %.200s", name, r.Status, r.Body)
	}
	if body := string(r.Body); !strings.Contains(body, own) || strings.Contains(body, other) {
		t.Fatalf("%s: own item %v, other tenant's item %v; want only its own",
			name, strings.Contains(body, own), strings.Contains(body, other))
	}
}

func signInUnavailable(t *testing.T, name, loginURL string) {
	t.Helper()
	start := time.Now()
	resp, body := newBrowser(t).get(loginURL)
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("%s sign-in took %s, want at most 10 s", name, took)
	}
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `id="sign-in-unavailable"`) {
		t.Errorf("%s sign-in with Zitadel down: status %d, body %.200s; want 503 and the unavailable page", name, resp.StatusCode, body)
	}
}

// Verifies: gateway-auth AC-3, AC-4, AC-10, AC-11; T-01, T-12
func TestTokensWorkWhileZitadelIsDown(t *testing.T) {
	if testing.Short() {
		t.Skip("takes Zitadel down for minutes")
	}
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	tokA, tokB := token(t, tenA), token(t, tenB)
	itemA := createItem(t, tokA, "outage-a-"+randomID())
	itemB := createItem(t, tokB, "outage-b-"+randomID())
	_, login, password := invitedHuman(t, iamAdmin, iamAdmin, tenA.OrgID)
	web := newBrowser(t)
	web.signIn(login, password)
	runJWKSSync(t)

	t.Cleanup(func() {
		zitadelUp(t)
		kube(t, 3*time.Minute, "-n", "gateway", "rollout", "status", "deploy/api-gateway", "--timeout=170s")
	})
	zitadelDown(t)
	down := time.Now()

	t.Run("issued tokens", func(t *testing.T) {
		ownItemsOnly(t, "tenant A", tokA, itemA.ID, itemB.ID)
		ownItemsOnly(t, "tenant B", tokB, itemB.ID, itemA.ID)
	})
	t.Run("restarted gateway", func(t *testing.T) {
		kube(t, time.Minute, "-n", "gateway", "rollout", "restart", "deploy/api-gateway")
		kube(t, 3*time.Minute, "-n", "gateway", "rollout", "status", "deploy/api-gateway", "--timeout=170s")
		for range 6 {
			ownItemsOnly(t, "tenant A via restarted pods", tokA, itemA.ID, itemB.ID)
			ownItemsOnly(t, "tenant B via restarted pods", tokB, itemB.ID, itemA.ID)
		}
	})
	t.Run("sign-in", func(t *testing.T) {
		signInUnavailable(t, "exampleweb", exampleWebURL+"/auth/login")
		signInUnavailable(t, "admin app", adminWebURL+"/auth/login")
	})
	t.Run("signed-in exampleweb user", func(t *testing.T) {
		resp, page := web.get(exampleWebURL + "/")
		if resp.StatusCode != http.StatusOK || !strings.Contains(page, itemA.ID) {
			t.Fatalf("page during the outage: status %d, has the user's item %v; body %.300s", resp.StatusCode, strings.Contains(page, itemA.ID), page)
		}
		if strings.Contains(page, `id="manage-users"`) {
			t.Error("the Manage users link shows while Zitadel is down")
		}
	})

	for time.Since(down) < outageFor {
		ownItemsOnly(t, "tenant A during the outage", tokA, itemA.ID, itemB.ID)
		time.Sleep(10 * time.Second)
	}
	zitadelUp(t)
	ownItemsOnly(t, "tenant A after the outage", tokA, itemA.ID, itemB.ID)
}

// Verifies: gateway-auth AC-8; T-04, T-12
func TestOnlySyncJobWritesKeyCache(t *testing.T) {
	can := func(verb, resource, as string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, _ := kubectl(ctx, "auth", "can-i", verb, resource, "-n", "gateway", "--as=system:serviceaccount:"+as)
		return strings.TrimSpace(out) == "yes"
	}
	if !can("update", "configmap/"+jwksConfigMap, "gateway:jwks-sync") {
		t.Fatal("the sync job may not update the cache, so the denials below prove nothing")
	}
	for _, tc := range []struct{ verb, resource, as string }{
		{"update", "configmap/" + jwksConfigMap, "gateway:default"},
		{"patch", "configmap/" + jwksConfigMap, "gateway:default"},
		{"update", "configmap/" + jwksConfigMap, "exampleweb:default"},
		{"update", "configmap/other", "gateway:jwks-sync"},
		{"create", "configmaps", "gateway:jwks-sync"},
		{"delete", "configmap/" + jwksConfigMap, "gateway:jwks-sync"},
	} {
		if can(tc.verb, tc.resource, tc.as) {
			t.Errorf("%s may %s %s", tc.as, tc.verb, tc.resource)
		}
	}
}

// Verifies: gateway-auth AC-9
func TestSyncJobMirrorsZitadelKeys(t *testing.T) {
	runJWKSSync(t)
	cached, published := cachedKeyIDs(t), zitadelKeyIDs(t)
	for _, kid := range published {
		if !slices.Contains(cached, kid) {
			t.Errorf("Zitadel's key %s is not in the cache %v", kid, cached)
		}
	}
	if schedule := kube(t, time.Minute, "-n", "gateway", "get", "cronjob", "jwks-sync", "-o", "jsonpath={.spec.schedule}"); schedule != "* * * * *" {
		t.Errorf("jwks-sync runs on %q, want every minute", schedule)
	}
}
