// Package ui leaves role checks to the API; the SPA shows access denied on its 403.
package ui

import (
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
)

// dist/.gitkeep lets the package compile before the first `npm run build`.
//
//go:embed all:app/dist
var app embed.FS

// Inline styles stay allowed because Radix UI injects <style> elements.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"frame-ancestors 'none'; base-uri 'none'; object-src 'none'"

type Service struct {
	Sessions core.SessionReader
	Files    fs.FS
	Log      *slog.Logger
}

func NewService(sessions core.SessionReader, log *slog.Logger) *Service {
	files, err := fs.Sub(app, "app/dist")
	if err != nil {
		panic(err)
	}
	return &Service{Sessions: sessions, Files: files, Log: log}
}

func (s *Service) Handler(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.Sessions.Session(r); !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if info, err := fs.Stat(s.Files, name); err == nil && !info.IsDir() && name != "index.html" {
		// Vite puts a content hash into the names under assets/.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		}
		http.ServeFileFS(w, r, s.Files, name) //nolint:gosec // G703: name is cleaned and s.Files is an fs.FS, which rejects paths outside its root.
		return
	}

	// Any other path is a route of the SPA's router.
	index, err := fs.ReadFile(s.Files, "index.html")
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "the SPA is not built: run npm run build in apps/admin/ui/app", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		s.Log.ErrorContext(r.Context(), "index.html not read", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(index)
}
