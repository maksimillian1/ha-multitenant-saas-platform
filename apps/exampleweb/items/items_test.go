package items

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func gateway(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/items" || r.Header.Get("Authorization") != "Bearer user-token" {
			t.Errorf("request %s %s with %q, want GET /items with the user's token", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{GatewayURL: srv.URL, HTTP: srv.Client()}
}

// Verifies: T-02
func TestListDecodesExampleAppAnswer(t *testing.T) {
	// Shape of ExampleApp's GET /items answer.
	got, err := gateway(t, http.StatusOK, `{"items":[{"id":"7c9e","tenant_id":"org-a","name":"first","created_at":"2026-11-23T10:00:00Z"}]}`).
		List(context.Background(), "user-token")
	if err != nil || len(got) != 1 || got[0].ID != "7c9e" || got[0].Name != "first" {
		t.Errorf("List = %v, %v; want one item 7c9e", got, err)
	}
}

// Verifies: tenant-self-admin AC-11; T-02
func TestListErrors(t *testing.T) {
	for _, tc := range []struct {
		status       int
		body         string
		unauthorized bool
	}{
		{http.StatusUnauthorized, `{"error":"invalid token"}`, true},
		{http.StatusOK, `{"items":[`, false},
		{http.StatusBadGateway, `upstream down`, false},
	} {
		_, err := gateway(t, tc.status, tc.body).List(context.Background(), "user-token")
		if err == nil || errors.Is(err, ErrUnauthorized) != tc.unauthorized {
			t.Errorf("status %d: err %v, want an error (unauthorized=%v)", tc.status, err, tc.unauthorized)
		}
	}
}
