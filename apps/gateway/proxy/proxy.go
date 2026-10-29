// Package proxy is the request path of the API Gateway: authenticate the
// caller, set the tenant header and forward to the service that owns the path
// (docs/contracts/gateway-headers.md).
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/core"
)

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

type Service struct {
	Auth   core.Authenticator
	Routes []Route
	Usage  core.UsageReporter
	Log    *slog.Logger
	proxy  *httputil.ReverseProxy
}

type upstreamKey struct{}

// NewService returns the gateway. A nil reporter disables usage reporting.
func NewService(authn core.Authenticator, routes []Route, reporter core.UsageReporter, log *slog.Logger) *Service {
	// Longest prefix first, so "/items/archive" wins over "/items".
	routes = append([]Route(nil), routes...)
	sort.Slice(routes, func(i, j int) bool { return len(routes[i].Prefix) > len(routes[j].Prefix) })

	s := &Service{Auth: authn, Routes: routes, Usage: reporter, Log: log}
	s.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(pr.In.Context().Value(upstreamKey{}).(*url.URL))
			pr.SetXForwarded()
			// The JWT stays at the gateway; services get only the tenant.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Set(core.TenantHeader, pr.In.Header.Get(core.TenantHeader))
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			MaxIdleConnsPerHost:   256,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.ErrorContext(r.Context(), "upstream failed", "path", r.URL.Path, "err", err)
			core.WriteError(w, http.StatusBadGateway, "upstream unavailable")
		},
	}
	return s
}

func (s *Service) match(path string) *Route {
	for i := range s.Routes {
		p := s.Routes[i].Prefix
		if p == "" || path == p || strings.HasPrefix(path, p+"/") {
			return &s.Routes[i]
		}
	}
	return nil
}

func (s *Service) Handler(w http.ResponseWriter, r *http.Request) {
	// Never trust a tenant header from the client.
	r.Header.Del(core.TenantHeader)

	token, err := auth.BearerToken(r)
	if err != nil {
		core.WriteError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	tenant, err := s.Auth.Tenant(r.Context(), token)
	if err != nil {
		s.Log.InfoContext(r.Context(), "rejected token", "path", r.URL.Path, "err", err)
		core.WriteError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	// Every request served for a tenant is one usage event, whatever its
	// status. Requests rejected with 401 above have no tenant and none.
	if s.Usage != nil {
		start := time.Now()
		rec := &usageRecorder{ResponseWriter: w, status: http.StatusOK}
		body := &countingBody{ReadCloser: r.Body}
		r.Body, w = body, rec
		defer func() {
			// URL.Path carries no query string; headers and bodies are never reported.
			s.Usage.Report(core.APICallEvent(tenant, start, r.Method, r.URL.Path, rec.status,
				time.Since(start), body.n, rec.bytes))
		}()
	}

	// Routes are matched only after authentication, so unknown paths do not
	// reveal which services exist.
	route := s.match(r.URL.Path)
	if route == nil {
		core.WriteError(w, http.StatusNotFound, "no such route")
		return
	}
	r.Header.Set(core.TenantHeader, tenant)
	s.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), upstreamKey{}, route.Upstream)))
}

// usageRecorder notes the status and the response bytes for the usage event.
type usageRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (u *usageRecorder) WriteHeader(code int) {
	if !u.wroteHeader {
		u.status, u.wroteHeader = code, true
	}
	u.ResponseWriter.WriteHeader(code)
}

func (u *usageRecorder) Write(b []byte) (int, error) {
	u.wroteHeader = true
	n, err := u.ResponseWriter.Write(b)
	u.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController (used by ReverseProxy to flush) reach
// the real writer.
func (u *usageRecorder) Unwrap() http.ResponseWriter { return u.ResponseWriter }

// countingBody counts the request bytes read by the upstream.
type countingBody struct {
	io.ReadCloser
	n int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}
