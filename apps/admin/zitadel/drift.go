package zitadel

import (
	"context"
	"log/slog"
	"time"
)

// An ERROR log line until alerts exist.
const DriftEvent = "service_account_credential_drift"

// Keys are issued for 30 days; an hour of slack covers the issuer's clock.
const MaxKeyValidity = 30*24*time.Hour + time.Hour

type Account struct {
	Name     string
	UserID   string // looked up by Name when empty
	OwnKeyID string // empty: the app holds none
}

type DriftFinding struct {
	Kind, ID, Reason string
}

// Any PAT, a key other than OwnKeyID (or any second key when the app holds none), or a too-long key.
func Drift(a Account, creds []Credential, now time.Time, maxValidity time.Duration) []DriftFinding {
	var out []DriftFinding
	keys := 0
	for _, c := range creds {
		if c.Kind == CredentialKey {
			keys++
		}
	}
	for _, c := range creds {
		switch {
		case c.Kind == CredentialPersonalAccessToken:
			out = append(out, DriftFinding{c.Kind, c.ID, "a personal access token"})
			continue
		case a.OwnKeyID != "" && c.ID != a.OwnKeyID:
			out = append(out, DriftFinding{c.Kind, c.ID, "a key the admin app does not use"})
		case a.OwnKeyID == "" && keys > 1:
			out = append(out, DriftFinding{c.Kind, c.ID, "one of several keys"})
		}
		if c.Expires.IsZero() || c.Expires.After(now.Add(maxValidity)) {
			out = append(out, DriftFinding{c.Kind, c.ID, "a key valid for more than 30 days"})
		}
	}
	return out
}

// Never logs a secret: Zitadel's listings carry none.
type DriftCheck struct {
	Client   *Client
	Accounts []Account
	Log      *slog.Logger
	Now      func() time.Time
}

func (d *DriftCheck) Run(ctx context.Context) {
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	for _, a := range d.Accounts {
		if a.UserID == "" {
			id, err := d.Client.UserIDByName(ctx, a.Name)
			if err != nil || id == "" {
				d.Log.WarnContext(ctx, "service_account_credential_check_failed", "account", a.Name, "err", errOrNotFound(err))
				continue
			}
			a.UserID = id
		}
		creds, err := d.Client.ListCredentials(ctx, a.UserID)
		if err != nil {
			d.Log.WarnContext(ctx, "service_account_credential_check_failed", "account", a.Name, "user_id", a.UserID, "err", err)
			continue
		}
		for _, f := range Drift(a, creds, now(), MaxKeyValidity) {
			d.Log.ErrorContext(ctx, DriftEvent, "account", a.Name, "user_id", a.UserID,
				"credential", f.Kind, "credential_id", f.ID, "reason", f.Reason)
		}
	}
}

func (d *DriftCheck) Loop(ctx context.Context, interval time.Duration) {
	d.Run(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.Run(ctx)
		}
	}
}

func errOrNotFound(err error) any {
	if err != nil {
		return err
	}
	return "no such user"
}
