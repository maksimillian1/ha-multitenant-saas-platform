package jwkssync

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

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/auth"
)

const (
	DataKey  = "jwks.json"
	saDir    = "/var/run/secrets/kubernetes.io/serviceaccount"
	maxReply = 1 << 20
)

type ConfigMap struct {
	baseURL   string
	namespace string
	name      string
	token     func() (string, error)
	http      *http.Client
}

func NewConfigMap(baseURL, namespace, name string, token func() (string, error), httpClient *http.Client) *ConfigMap {
	return &ConfigMap{baseURL: strings.TrimSuffix(baseURL, "/"), namespace: namespace, name: name, token: token, http: httpClient}
}

func InCluster(namespace, name string) (*ConfigMap, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("jwks-sync: not in a cluster")
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("jwks-sync: reading the cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("jwks-sync: no certificate in the cluster CA file")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	token := func() (string, error) {
		b, err := os.ReadFile(saDir + "/token")
		return strings.TrimSpace(string(b)), err
	}
	return NewConfigMap("https://"+net.JoinHostPort(host, port), namespace, name, token,
		&http.Client{Timeout: 10 * time.Second, Transport: transport}), nil
}

type configMap struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   map[string]any    `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
}

func (c *ConfigMap) path() string {
	return "/api/v1/namespaces/" + c.namespace + "/configmaps/" + c.name
}

func (c *ConfigMap) Load(ctx context.Context) (auth.CacheFile, string, error) {
	body, err := c.send(ctx, http.MethodGet, nil)
	if err != nil {
		return auth.CacheFile{}, "", err
	}
	var cm configMap
	if err := json.Unmarshal(body, &cm); err != nil {
		return auth.CacheFile{}, "", fmt.Errorf("configmap: %w", err)
	}
	version, _ := cm.Metadata["resourceVersion"].(string)
	var cache auth.CacheFile
	if raw := cm.Data[DataKey]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &cache); err != nil || auth.CheckKeys(cache.Keys) != nil {
			return auth.CacheFile{}, version, nil
		}
	}
	return cache, version, nil
}

func (c *ConfigMap) Save(ctx context.Context, cache auth.CacheFile, version string) error {
	if err := auth.CheckKeys(cache.Keys); err != nil {
		return err
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	_, err = c.send(ctx, http.MethodPut, configMap{
		APIVersion: "v1", Kind: "ConfigMap",
		Metadata: map[string]any{"name": c.name, "namespace": c.namespace, "resourceVersion": version},
		Data:     map[string]string{DataKey: string(data)},
	})
	return err
}

func (c *ConfigMap) send(ctx context.Context, method string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+c.path(), reader)
	if err != nil {
		return nil, err
	}
	token, err := c.token()
	if err != nil {
		return nil, fmt.Errorf("configmap: reading the service account token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("configmap: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return nil, fmt.Errorf("configmap: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("configmap: %s %s: status %d: %.200s", method, c.path(), resp.StatusCode, b)
	}
	return b, nil
}
