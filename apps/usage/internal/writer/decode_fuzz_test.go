package writer

import (
	"bytes"
	"context"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/contracttest"
)

// Any pod with usage-writeapi's login, or a bug in it, can write such bytes.
// Verifies: usage-writer AC-1; AC-2; T-11
func FuzzDecode(f *testing.F) {
	for _, v := range contracttest.Consumes(f, "kafka", "writer") {
		for _, e := range contracttest.Examples(f, "kafka", v) {
			f.Add([]byte(e))
		}
	}
	for _, seed := range []string{
		``, `null`, `[]`, `{}`, `{"schema_version":1}`, `{"schema_version":-1}`, `{"schema_version":99}`,
		`{"schema_version":1,"event_id":"x","tenant_id":"t","event_time":"2026-01-01T00:00:00Z"}`,
		`{"schema_version":1,"event_id":"0192f6a8-7c1e-7b3a-9c4d-2f1e8a6b5c3d","event_time":"2026-10-24T20:15:03Z",` +
			`"tenant_id":"t","metric":"api_calls","quantity":1,"attributes":{"p":"\u0000"}}`,
		`{"schema_version":1,"quantity":9223372036854775808}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, value []byte) {
		w := &Service{Log: quiet}
		events, poison := w.decode(context.Background(), records(value))
		if len(events)+len(poison) != 1 {
			t.Fatalf("one record became %d events and %d poison", len(events), len(poison))
		}
		if len(poison) == 1 {
			if !bytes.Equal(poison[0].Record.Value, value) || poison[0].Reason == "" {
				t.Fatalf("poison changed the bytes or has no reason: %+v", poison[0])
			}
			return
		}
		if err := events[0].event.ValidateContent(); err != nil {
			t.Fatalf("accepted an event that breaks the contract: %v", err)
		}
	})
}
