package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var driftNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// credentialAnswers is what the fake Zitadel lists for one machine user.
func credentialAnswers(keys map[string]time.Time, pats ...string) map[string]string {
	list := func(items []string) string { return `{"result":[` + strings.Join(items, ",") + `]}` }
	var k, p []string
	for id, exp := range keys {
		if exp.IsZero() {
			k = append(k, fmt.Sprintf(`{"id":%q,"userId":"u"}`, id))
			continue
		}
		k = append(k, fmt.Sprintf(`{"id":%q,"userId":"u","expirationDate":%q}`, id, exp.Format(time.RFC3339)))
	}
	for _, id := range pats {
		p = append(p, fmt.Sprintf(`{"id":%q,"userId":"u","expirationDate":%q}`, id, driftNow.Add(time.Hour).Format(time.RFC3339)))
	}
	return map[string]string{
		"/" + userService + "/ListKeys":                 list(k),
		"/" + userService + "/ListPersonalAccessTokens": list(p),
		"/" + userService + "/ListUsers":                `{"result":[{"userId":"user-iam-admin"}]}`,
	}
}

type driftEvent struct {
	Level        string `json:"level"`
	Msg          string `json:"msg"`
	Account      string `json:"account"`
	UserID       string `json:"user_id"`
	Credential   string `json:"credential"`
	CredentialID string `json:"credential_id"`
	Reason       string `json:"reason"`
}

// runDrift runs the check for one account against a fake Zitadel and returns
// the logged events.
func runDrift(t *testing.T, a Account, answers map[string]string) ([]driftEvent, *fakeZitadel) {
	t.Helper()
	f, c := newFake(t, answers)
	var buf bytes.Buffer
	d := &DriftCheck{Client: c, Accounts: []Account{a}, Log: slog.New(slog.NewJSONHandler(&buf, nil)),
		Now: func() time.Time { return driftNow }}
	d.Run(context.Background())
	var events []driftEvent
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var e driftEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		events = append(events, e)
	}
	return events, f
}

func findings(events []driftEvent) []string {
	var out []string
	for _, e := range events {
		if e.Level != "ERROR" || e.Msg != DriftEvent {
			out = append(out, e.Level+" "+e.Msg)
			continue
		}
		out = append(out, e.Account+"/"+e.Credential+"/"+e.CredentialID+": "+e.Reason)
	}
	return out
}

// Verifies: admin-zitadel AC-8; T-02
func TestCredentialDrift(t *testing.T) {
	app := Account{Name: "admin-app", UserID: "user-admin-app", OwnKeyID: "own"}
	in30 := driftNow.Add(29 * 24 * time.Hour)
	for _, tc := range []struct {
		name    string
		answers map[string]string
		want    []string
	}{
		{"only its own key", credentialAnswers(map[string]time.Time{"own": in30}), nil},
		{"a personal access token", credentialAnswers(map[string]time.Time{"own": in30}, "pat-1"),
			[]string{"admin-app/personal_access_token/pat-1: a personal access token"}},
		{"a key the app does not use", credentialAnswers(map[string]time.Time{"own": in30, "other": in30}),
			[]string{"admin-app/key/other: a key the admin app does not use"}},
		{"its key valid for a year", credentialAnswers(map[string]time.Time{"own": driftNow.AddDate(1, 0, 0)}),
			[]string{"admin-app/key/own: a key valid for more than 30 days"}},
		{"its key without expiry", credentialAnswers(map[string]time.Time{"own": {}}),
			[]string{"admin-app/key/own: a key valid for more than 30 days"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, f := runDrift(t, app, tc.answers)
			if got := findings(events); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("events = %q, want %q", got, tc.want)
			}
			for _, r := range f.requests {
				if !strings.HasSuffix(r.path, "/ListKeys") && !strings.HasSuffix(r.path, "/ListPersonalAccessTokens") {
					continue
				}
				want := jsonValue(t, `{"pagination":{"limit":100,"asc":true},"filters":[{"userIdFilter":{"id":"user-admin-app"}}]}`)
				if !reflect.DeepEqual(r.body, want) {
					t.Errorf("%s body = %v, want %v", r.path, r.body, want)
				}
			}
		})
	}
}

// Verifies: admin-zitadel AC-13
func TestCredentialDriftIAMAdmin(t *testing.T) {
	iamAdmin := Account{Name: "iam-admin"} // found by name; the app holds no key of it
	in30 := driftNow.Add(29 * 24 * time.Hour)
	for _, tc := range []struct {
		name    string
		answers map[string]string
		want    []string
	}{
		{"one 30-day key", credentialAnswers(map[string]time.Time{"k1": in30}), nil},
		{"the chart's PAT", credentialAnswers(map[string]time.Time{"k1": in30}, "pat-chart"),
			[]string{"iam-admin/personal_access_token/pat-chart: a personal access token"}},
		{"the chart's key until 2029", credentialAnswers(map[string]time.Time{"k1": time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)}),
			[]string{"iam-admin/key/k1: a key valid for more than 30 days"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, f := runDrift(t, iamAdmin, tc.answers)
			if got := findings(events); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("events = %q, want %q", got, tc.want)
			}
			for _, e := range events {
				if e.Msg == DriftEvent && e.UserID != "user-iam-admin" {
					t.Errorf("user_id = %q, want the ID found by name", e.UserID)
				}
			}
			want := jsonValue(t, `{"query":{"limit":2,"asc":true},"queries":[{"userNameQuery":{"userName":"iam-admin","method":"TEXT_QUERY_METHOD_EQUALS"}}]}`)
			if got := f.requests[0]; !strings.HasSuffix(got.path, "/ListUsers") || !reflect.DeepEqual(got.body, want) {
				t.Errorf("first request %s %v, want ListUsers %v", got.path, got.body, want)
			}
		})
	}
	t.Run("two keys", func(t *testing.T) {
		events, _ := runDrift(t, iamAdmin, credentialAnswers(map[string]time.Time{"k1": in30, "k2": in30}))
		got := findings(events)
		sort.Strings(got) // the keys come in any order
		want := []string{"iam-admin/key/k1: one of several keys", "iam-admin/key/k2: one of several keys"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("events = %q, want %q", got, want)
		}
	})
	t.Run("no such user", func(t *testing.T) {
		answers := credentialAnswers(nil)
		answers["/"+userService+"/ListUsers"] = `{"result":[]}`
		events, _ := runDrift(t, iamAdmin, answers)
		if got := findings(events); len(got) != 1 || got[0] != "WARN service_account_credential_check_failed" {
			t.Errorf("events = %q, want one check failure", got)
		}
	})
}

// A broken answer to a credential listing is an error: the check logs that it
// failed and reports no finding.
//
// Verifies: admin-zitadel T-02
func TestBrokenCredentialAnswer(t *testing.T) {
	app := Account{Name: "admin-app", UserID: "user-admin-app", OwnKeyID: "own"}
	for name, broken := range map[string]map[string]string{
		"keys not JSON":       {"/" + userService + "/ListKeys": `{"result":[`},
		"tokens not JSON":     {"/" + userService + "/ListPersonalAccessTokens": `[1,2`},
		"result not a list":   {"/" + userService + "/ListKeys": `{"result":"x"}`},
		"bad expiration date": {"/" + userService + "/ListKeys": `{"result":[{"id":"own","expirationDate":"soon"}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			answers := credentialAnswers(map[string]time.Time{"own": driftNow.Add(time.Hour)})
			for k, v := range broken {
				answers[k] = v
			}
			_, c := newFake(t, answers)
			if creds, err := c.ListCredentials(context.Background(), app.UserID); err == nil {
				t.Errorf("ListCredentials = %+v, want an error", creds)
			}
			events, _ := runDrift(t, app, answers)
			if got := findings(events); len(got) != 1 || got[0] != "WARN service_account_credential_check_failed" {
				t.Errorf("events = %q, want one check failure", got)
			}
		})
	}
}
