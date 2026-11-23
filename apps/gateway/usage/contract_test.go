package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/core"
)

const contracts = "../../../docs/contracts/usage-event"

// Only kinds, so a renamed, missing, extra or retyped field changes the shape while values do not.
func shape(v any) any {
	switch v := v.(type) {
	case map[string]any:
		s := map[string]any{}
		for k, x := range v {
			s[k] = shape(x)
		}
		return s
	case []any:
		if len(v) == 0 {
			return "array"
		}
		return []any{shape(v[0])}
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case bool:
		return "bool"
	default:
		return "null"
	}
}

func decodeJSON(t *testing.T, r io.Reader) any {
	t.Helper()
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func examples(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(contracts + "/participants.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		HTTP struct {
			Schema       string `json:"schema"`
			Participants map[string]struct {
				Produces []string `json:"produces"`
			} `json:"participants"`
		} `json:"http"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	produces := m.HTTP.Participants["gateway"].Produces
	if len(produces) == 0 {
		t.Fatal("participants.json: gateway produces no HTTP version")
	}
	out := map[string]any{}
	for _, v := range produces {
		f, err := os.Open(fmt.Sprintf("%s/schemas/%s.%s.json", contracts, m.HTTP.Schema, v))
		if err != nil {
			t.Fatal(err)
		}
		schema := decodeJSON(t, f).(map[string]any)
		f.Close()
		ex, ok := schema["examples"].([]any)
		if !ok || len(ex) == 0 {
			t.Fatalf("%s request schema has no examples", v)
		}
		out[v] = ex[0]
	}
	return out
}

func TestSentEventMatchesDeclaredVersion(t *testing.T) {
	for v, example := range examples(t) {
		t.Run(v, func(t *testing.T) {
			want := example.(map[string]any)
			want["events"] = want["events"].([]any)[:1]

			bodies := make(chan any, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bodies <- decodeJSON(t, r.Body)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer srv.Close()

			r := NewService(srv.URL+"/"+v+"/events", 10, quiet)
			r.Report(core.APICallEvent("tenant-a", time.Now(), http.MethodGet, "/items/42", 200, 12*time.Millisecond, 0, 87))
			sent := <-bodies
			r.Close(context.Background())

			if g, w := mustJSON(t, shape(sent)), mustJSON(t, shape(want)); g != w {
				t.Fatalf("sent body has shape %s\nwant the example's %s", g, w)
			}
		})
	}
}

// encoding/json sorts map keys, so equal shapes print equally.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
