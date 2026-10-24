// Package proxy is the request path of the API Gateway: authenticate the
// caller, set the tenant header and forward to the service that owns the path
// (docs/contracts/gateway-headers.md).
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/internal/auth"
)

const TenantHeader = "x-tenant-id"

// Authenticator returns the tenant of a valid access token.
type Authenticator interface {
	Tenant(ctx context.Context, token string) (string, error)
}

// Route sends every path equal to Prefix or below it ("/items", "/items/1",
// not "/itemsx") to Upstream, keeping the path.
type Route struct {
	Prefix   string
	Upstream *url.URL
}

// ParseRoutes reads "prefix=url,prefix=url", e.g. "/items=http://exampleapp.exampleapp.svc:8080".
func ParseRoutes(spec string) ([]Route, error) {
	var routes []Route
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, raw, ok := strings.Cut(part, "=")
		if !ok || !strings.HasPrefix(prefix, "/") {
			return nil, fmt.Errorf("route %q: want /prefix=http://host:port", part)
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("route %q: bad upstream URL", part)
		}
		routes = append(routes, Route{Prefix: strings.TrimSuffix(prefix, "/"), Upstream: u})
	}
	if len(routes) == 0 {
		return nil, errors.New("no routes configured")
	}
	return routes, nil
}

type Gateway struct {
	auth   Authenticator
	routes []Route
	proxy  *httputil.ReverseProxy
	log    *slog.Logger
}

type upstreamKey struct{}

func New(authn Authenticator, routes []Route, log *slog.Logger) *Gateway {
	// Longest prefix first, so "/items/archive" wins over "/items".
	routes = append([]Route(nil), routes...)
	sort.Slice(routes, func(i, j int) bool { return len(routes[i].Prefix) > len(routes[j].Prefix) })

	g := &Gateway{auth: authn, routes: routes, log: log}
	g.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(pr.In.Context().Value(upstreamKey{}).(*url.URL))
			pr.SetXForwarded()
			// The JWT stays at the gateway; services get only the tenant.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Set(TenantHeader, pr.In.Header.Get(TenantHeader))
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			MaxIdleConnsPerHost:   256,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.ErrorContext(r.Context(), "upstream failed", "path", r.URL.Path, "err", err)
			writeError(w, http.StatusBadGateway, "upstream unavailable")
		},
	}
	return g
}

func (g *Gateway) match(path string) *Route {
	for i := range g.routes {
		p := g.routes[i].Prefix
		if p == "" || path == p || strings.HasPrefix(path, p+"/") {
			return &g.routes[i]
		}
	}
	return nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Never trust a tenant header from the client.
	r.Header.Del(TenantHeader)

	token, err := auth.BearerToken(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	tenant, err := g.auth.Tenant(r.Context(), token)
	if err != nil {
		g.log.InfoContext(r.Context(), "rejected token", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	// Routes are matched only after authentication, so unknown paths do not
	// reveal which services exist.
	route := g.match(r.URL.Path)
	if route == nil {
		writeError(w, http.StatusNotFound, "no such route")
		return
	}
	r.Header.Set(TenantHeader, tenant)
	g.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), upstreamKey{}, route.Upstream)))
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
