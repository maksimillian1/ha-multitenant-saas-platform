package writeapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/contracttest"
)

// The examples carry fixed event times; the service's clock is set next to them.
var exampleNow = time.Date(2026, 10, 24, 20, 16, 0, 0, time.UTC)

// UsageWriteAPI serves every HTTP version it declares in participants.json and
// accepts that version's examples, with attributes reaching the producer byte
// for byte, so 2^53 + 1 is not rounded.
func TestAcceptsEveryConsumedVersion(t *testing.T) {
	for _, v := range contracttest.Consumes(t, "http", "writeapi") {
		for i, example := range contracttest.Examples(t, "http", v) {
			t.Run(v, func(t *testing.T) {
				p := &fakeProducer{}
				s := NewService(p, quiet)
				s.now = func() time.Time { return exampleNow }
				mux := http.NewServeMux()
				s.Register(mux)

				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/"+v+"/events", bytes.NewReader(example)))
				if w.Code != http.StatusAccepted {
					t.Fatalf("example %d: status %d: %s", i, w.Code, w.Body)
				}

				var sent struct {
					Events []struct{ Attributes json.RawMessage }
				}
				if err := json.Unmarshal(example, &sent); err != nil {
					t.Fatal(err)
				}
				if len(p.got) != len(sent.Events) {
					t.Fatalf("example %d: produced %d events, want %d", i, len(p.got), len(sent.Events))
				}
				for j, e := range sent.Events {
					if !bytes.Equal(compact(t, p.got[j].Attributes), compact(t, e.Attributes)) {
						t.Fatalf("example %d event %d: attributes = %s, want %s", i, j, p.got[j].Attributes, e.Attributes)
					}
				}
			})
		}
	}
}

// compact drops the whitespace of the example's layout, nothing else.
func compact(t *testing.T, b []byte) []byte {
	t.Helper()
	if len(b) == 0 {
		return nil
	}
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
