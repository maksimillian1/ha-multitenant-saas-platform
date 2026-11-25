// Package page renders the tenant's items and, for its admins, a link to the Zitadel Console.
package page

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/items"
)

type Sessions interface {
	Session(r *http.Request) (string, core.Session, error)
	Refresh(ctx context.Context, id string, session core.Session) (core.Session, error)
}

type ItemLister interface {
	List(ctx context.Context, accessToken string) ([]items.Item, error)
}

type OwnerChecker interface {
	IsOrgOwner(ctx context.Context, accessToken, orgID string) (bool, error)
}

type Service struct {
	Sessions   Sessions
	Items      ItemLister
	Owners     OwnerChecker
	ConsoleURL string
	Log        *slog.Logger
	Now        func() time.Time
}

var tmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>ExampleApp</title></head>
<body>
<header>
  <p>Signed in as {{.Name}}</p>
  {{if .ConsoleURL}}<p><a id="manage-users" href="{{.ConsoleURL}}">Manage users</a></p>{{end}}
  <form method="post" action="/auth/logout"><button type="submit">Sign out</button></form>
</header>
<main>
  <h1>Items</h1>
  {{if .Items}}<ul>{{range .Items}}<li data-id="{{.ID}}">{{.Name}}</li>{{end}}</ul>{{else}}<p>No items yet.</p>{{end}}
</main>
</body>
</html>
`))

type view struct {
	Name       string
	ConsoleURL string
	Items      []items.Item
}

func (s *Service) Handler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, session, err := s.Sessions.Session(r)
	if err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}

	if s.halfSpent(session) {
		if fresh, err := s.Sessions.Refresh(ctx, id, session); err != nil {
			s.Log.WarnContext(ctx, "early refresh failed, using the current token", "err", err)
		} else {
			session = fresh
		}
	}

	list, err := s.Items.List(ctx, session.AccessToken)
	if errors.Is(err, items.ErrUnauthorized) {
		// Access tokens live for minutes; refresh once, and sign in again when that fails too.
		session, err = s.Sessions.Refresh(ctx, id, session)
		if err != nil {
			s.Log.InfoContext(ctx, "refresh failed, signing in again", "err", err)
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		}
		list, err = s.Items.List(ctx, session.AccessToken)
	}
	if errors.Is(err, items.ErrUnauthorized) {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if err != nil {
		s.Log.ErrorContext(ctx, "items not read", "err", err)
		http.Error(w, "items are not available right now", http.StatusBadGateway)
		return
	}

	v := view{Name: session.Name, Items: list}
	owner, err := s.Owners.IsOrgOwner(ctx, session.AccessToken, session.OrgID)
	if err != nil {
		// Without the answer the link stays hidden; the Console checks the role itself anyway.
		s.Log.WarnContext(ctx, "memberships not read", "err", err)
	}
	if owner {
		v.ConsoleURL = s.ConsoleURL
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.Execute(w, v); err != nil {
		s.Log.ErrorContext(ctx, "page not rendered", "err", err)
	}
}

func (s *Service) halfSpent(session core.Session) bool {
	if session.AccessExpires.IsZero() || session.AccessIssued.IsZero() {
		return false
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	half := session.AccessIssued.Add(session.AccessExpires.Sub(session.AccessIssued) / 2)
	return !now().Before(half)
}
