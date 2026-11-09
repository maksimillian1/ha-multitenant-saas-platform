// Package kube creates and deletes a tenant's namespace through the
// Kubernetes REST API (ADR-0001, item 3; ADR-0012). It talks plain HTTP with
// JSON, without client-go, and needs RBAC for namespaces, resourcequotas and
// ciliumnetworkpolicies only.
//
// Every namespace it creates carries the tenant's ID as a label, and it never
// changes or deletes a namespace whose label names another tenant.
package kube

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
)

const (
	// TenantLabel holds the tenant ID on every tenant namespace.
	TenantLabel    = "hamsp.io/tenant-id"
	managedByLabel = "app.kubernetes.io/managed-by"
	managedBy      = "tenant-management"

	QuotaName  = "tenant-quota"
	PolicyName = "tenant-isolation"

	saDir    = "/var/run/secrets/kubernetes.io/serviceaccount"
	maxReply = 1 << 20
)

var (
	// ErrNotOurs: the namespace exists but belongs to another tenant or was
	// not created by Tenant Management.
	ErrNotOurs = errors.New("kube: the namespace belongs to someone else")
	// ErrTerminating: the namespace is still being deleted.
	ErrTerminating = errors.New("kube: the namespace is still being deleted")
)

// Quota is the ResourceQuota of every tenant namespace.
type Quota struct {
	CPU    string
	Memory string
	Pods   string
}

// Client calls one Kubernetes API server.
type Client struct {
	baseURL   string
	token     func() (string, error)
	http      *http.Client
	quota     Quota
	poll      time.Duration
	deleteFor time.Duration
}

// NewClient returns a client for the API server at baseURL with a bearer
// token from token (read again on every call: service account tokens rotate).
func NewClient(baseURL string, token func() (string, error), httpClient *http.Client, quota Quota) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"), token: token, http: httpClient, quota: quota,
		poll: time.Second, deleteFor: 2 * time.Minute,
	}
}

// InCluster returns a client for the cluster the pod runs in, with the pod's
// service account. baseURL overrides the API server's address when set.
func InCluster(baseURL string, timeout time.Duration, quota Quota) (*Client, error) {
	if baseURL == "" {
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if host == "" || port == "" {
			return nil, errors.New("kube: not in a cluster and KUBE_API is not set")
		}
		baseURL = "https://" + net.JoinHostPort(host, port)
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("kube: reading the cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("kube: no certificate in the cluster CA file")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	token := func() (string, error) {
		b, err := os.ReadFile(saDir + "/token")
		return strings.TrimSpace(string(b)), err
	}
	return NewClient(baseURL, token, &http.Client{Timeout: timeout, Transport: transport}, quota), nil
}

type meta struct {
	Name              string            `json:"name"`
	Labels            map[string]string `json:"labels,omitempty"`
	DeletionTimestamp *string           `json:"deletionTimestamp,omitempty"`
}

type object struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   meta   `json:"metadata"`
	Spec       any    `json:"spec,omitempty"`
}

func (c *Client) labels(tenantID string) map[string]string {
	return map[string]string{TenantLabel: tenantID, managedByLabel: managedBy}
}

// EnsureNamespace creates the tenant's namespace with its ResourceQuota and
// its default-deny CiliumNetworkPolicy. Parts that exist are kept.
func (c *Client) EnsureNamespace(ctx context.Context, tenantID string) error {
	ns := core.Namespace(tenantID)
	err := c.create(ctx, "/api/v1/namespaces", object{
		APIVersion: "v1", Kind: "Namespace",
		Metadata: meta{Name: ns, Labels: c.labels(tenantID)},
	})
	if errors.Is(err, errExists) {
		existing, getErr := c.get(ctx, "/api/v1/namespaces/"+ns)
		if getErr != nil {
			return getErr
		}
		if err := owned(existing, tenantID); err != nil {
			return err
		}
		if existing.Metadata.DeletionTimestamp != nil {
			return ErrTerminating
		}
	} else if err != nil {
		return err
	}

	quota := object{
		APIVersion: "v1", Kind: "ResourceQuota",
		Metadata: meta{Name: QuotaName, Labels: c.labels(tenantID)},
		Spec: map[string]any{"hard": map[string]string{
			"requests.cpu": c.quota.CPU, "limits.cpu": c.quota.CPU,
			"requests.memory": c.quota.Memory, "limits.memory": c.quota.Memory,
			"pods": c.quota.Pods,
		}},
	}
	if err := c.create(ctx, "/api/v1/namespaces/"+ns+"/resourcequotas", quota); err != nil && !errors.Is(err, errExists) {
		return err
	}

	if err := c.create(ctx, "/apis/cilium.io/v2/namespaces/"+ns+"/ciliumnetworkpolicies", isolationPolicy(c.labels(tenantID))); err != nil &&
		!errors.Is(err, errExists) {
		return err
	}
	return nil
}

// isolationPolicy lets the tenant's pods talk to each other, to cluster DNS
// and to the internet, and to nothing else in the cluster: no other tenant's
// namespace and no platform service. Traffic into the namespace comes only
// from the namespace itself. HAMSP-58 tests it across tenants (QS-02).
func isolationPolicy(labels map[string]string) object {
	sameNamespace := []map[string]any{{}}
	return object{
		APIVersion: "cilium.io/v2", Kind: "CiliumNetworkPolicy",
		Metadata: meta{Name: PolicyName, Labels: labels},
		Spec: map[string]any{
			"endpointSelector": map[string]any{},
			"ingress":          []map[string]any{{"fromEndpoints": sameNamespace}},
			"egress": []map[string]any{
				{"toEndpoints": sameNamespace},
				{
					"toEndpoints": []map[string]any{{"matchLabels": map[string]string{
						"k8s:io.kubernetes.pod.namespace": "kube-system", "k8s-app": "kube-dns",
					}}},
					"toPorts": []map[string]any{{"ports": []map[string]string{{"port": "53", "protocol": "ANY"}}}},
				},
				{"toEntities": []string{"world"}},
			},
		},
	}
}

// DeleteNamespace deletes the tenant's namespace, and with it everything in
// it, and waits until it is gone. A namespace that does not exist is deleted.
func (c *Client) DeleteNamespace(ctx context.Context, tenantID string) error {
	path := "/api/v1/namespaces/" + core.Namespace(tenantID)
	existing, err := c.get(ctx, path)
	if errors.Is(err, errNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := owned(existing, tenantID); err != nil {
		return err
	}
	if existing.Metadata.DeletionTimestamp == nil {
		if _, err := c.send(ctx, http.MethodDelete, path, nil); err != nil && !errors.Is(err, errNotFound) {
			return err
		}
	}
	deadline := time.Now().Add(c.deleteFor)
	for {
		_, err := c.get(ctx, path)
		if errors.Is(err, errNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return ErrTerminating
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.poll):
		}
	}
}

func owned(o object, tenantID string) error {
	if o.Metadata.Labels[TenantLabel] != tenantID || o.Metadata.Labels[managedByLabel] != managedBy {
		return fmt.Errorf("%w: %s", ErrNotOurs, o.Metadata.Name)
	}
	return nil
}

var (
	errExists   = errors.New("kube: already exists")
	errNotFound = errors.New("kube: not found")
)

// StatusError is an error answer of the API server.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string { return fmt.Sprintf("kube: %d: %s", e.Status, e.Message) }

func (c *Client) create(ctx context.Context, path string, o object) error {
	_, err := c.send(ctx, http.MethodPost, path, o)
	return err
}

func (c *Client) get(ctx context.Context, path string) (object, error) {
	data, err := c.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return object{}, err
	}
	var o object
	if err := json.Unmarshal(data, &o); err != nil {
		return object{}, fmt.Errorf("kube: decoding %s: %w", path, err)
	}
	return o, nil
}

func (c *Client) send(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	token, err := c.token()
	if err != nil {
		return nil, fmt.Errorf("kube: reading the service account token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kube: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxReply))
	if err != nil {
		return nil, fmt.Errorf("kube: reading %s: %w", path, err)
	}
	switch {
	case res.StatusCode == http.StatusConflict && method == http.MethodPost:
		return nil, errExists
	case res.StatusCode == http.StatusNotFound:
		return nil, errNotFound
	case res.StatusCode >= 300:
		var status struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &status)
		if status.Message == "" {
			status.Message = http.StatusText(res.StatusCode)
		}
		return nil, &StatusError{Status: res.StatusCode, Message: status.Message}
	}
	return data, nil
}
