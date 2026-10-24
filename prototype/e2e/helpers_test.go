// Package e2e tests the walking skeleton (HAMSP-5) on the local kind cluster:
// client → Cilium Gateway API → API Gateway (Go: JWT, x-tenant-id) →
// ExampleApp → PgBouncer → PostgreSQL with RLS. Run prototype/create-cluster.sh and
// prototype/bootstrap-tenants.sh first, then: go test -C prototype/e2e -count=1 ./...
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	apiBase      = "http://api.localtest.me:30080"
	tenantClaim  = "urn:zitadel:iam:user:resourceowner:id"
	tenantHeader = "x-tenant-id"
)

type tenant struct {
	Name         string `json:"name"`
	OrgID        string `json:"org_id"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type config struct {
	Issuer  string   `json:"issuer"`
	Gateway string   `json:"gateway"`
	Tenants []tenant `json:"tenants"`
}

type item struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
}

var (
	cfg  config
	tenA tenant
	tenB tenant
	// client sends every request to the gateway NodePort whatever the host
	// name, so *.localtest.me needs no DNS.
	client *http.Client
)

func loadConfig() error {
	path := os.Getenv("HAMSP_TENANTS")
	if path == "" {
		path = "../.tenants.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w (run prototype/bootstrap-tenants.sh first)", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if len(cfg.Tenants) < 2 {
		return fmt.Errorf("%s: need 2 tenants, got %d", path, len(cfg.Tenants))
	}
	tenA, tenB = cfg.Tenants[0], cfg.Tenants[1]
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, cfg.Gateway)
			},
			MaxIdleConnsPerHost: 100,
		},
	}
	return nil
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// token gets a fresh access token for the tenant's machine user.
func token(t testing.TB, ten tenant) string {
	t.Helper()
	tok, err := fetchToken(ten)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func fetchToken(ten tenant) (string, error) {
	form := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {"openid urn:zitadel:iam:user:resourceowner"},
	}
	req, _ := http.NewRequest(http.MethodPost, cfg.Issuer+"/oauth/v2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(ten.ClientID), url.QueryEscape(ten.ClientSecret))
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token for %s: %w", ten.Name, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token for %s: %d %s", ten.Name, resp.StatusCode, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

type response struct {
	Status int
	Body   []byte
}

func (r response) decode(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
}

// call sends a request to ExampleApp through the gateway. An empty bearer
// sends no Authorization header. Extra headers come in name, value pairs.
func call(t testing.TB, method, path, bearer string, body any, headers ...string) response {
	t.Helper()
	r, err := doCall(method, path, bearer, body, headers...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func doCall(method, path, bearer string, body any, headers ...string) (response, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return response{}, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, apiBase+path, rd)
	if err != nil {
		return response{}, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return response{Status: resp.StatusCode, Body: raw}, nil
}

func mustStatus(t testing.TB, r response, want int, what string) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("%s: status %d, want %d; body %s", what, r.Status, want, r.Body)
	}
}

func createItem(t testing.TB, bearer, name string) item {
	t.Helper()
	r := call(t, http.MethodPost, "/items", bearer, map[string]string{"name": name})
	mustStatus(t, r, http.StatusCreated, "POST /items")
	var it item
	r.decode(t, &it)
	return it
}

func listItems(t testing.TB, bearer string) []item {
	t.Helper()
	r := call(t, http.MethodGet, "/items", bearer, nil)
	mustStatus(t, r, http.StatusOK, "GET /items")
	var out struct {
		Items []item `json:"items"`
	}
	r.decode(t, &out)
	return out.Items
}

func containsID(items []item, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// jwtParts splits a JWT and decodes its payload.
func jwtParts(t testing.TB, tok string) (header, payload, sig string, claims map[string]any) {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", tok)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return parts[0], parts[1], parts[2], claims
}

// kubectl runs kubectl against the current context and returns stdout.
func kubectl(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// psql runs SQL as the postgres superuser on the primary, in the exampleapp database.
func psql(t testing.TB, sql string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	primary, err := kubectl(ctx, "-n", "database", "get", "cluster", "main-pg-cluster",
		"-o", "jsonpath={.status.currentPrimary}")
	if err != nil {
		t.Fatal(err)
	}
	out, err := kubectl(ctx, "-n", "database", "exec", primary, "-c", "postgres", "--",
		"psql", "-U", "postgres", "-d", "exampleapp", "-Atq", "-v", "ON_ERROR_STOP=1", "-c", sql)
	return strings.TrimSpace(out), err
}
