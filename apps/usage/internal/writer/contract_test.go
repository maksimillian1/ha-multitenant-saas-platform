package writer

import (
	"context"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/contracttest"
)

func records(values ...[]byte) []*kgo.Record {
	rs := make([]*kgo.Record, len(values))
	for i, v := range values {
		rs[i] = &kgo.Record{Value: v, Offset: int64(i)}
	}
	return rs
}

// Usage Writer reads the examples of every Kafka version it declares in
// participants.json.
func TestReadsEveryConsumedVersion(t *testing.T) {
	for _, v := range contracttest.Consumes(t, "kafka", "writer") {
		t.Run(v, func(t *testing.T) {
			w := &Service{Log: quiet}
			examples := contracttest.Examples(t, "kafka", v)
			values := make([][]byte, len(examples))
			for i, e := range examples {
				values[i] = e
			}
			if got := w.decode(context.Background(), records(values...)); len(got) != len(values) {
				t.Fatalf("decoded %d of %d examples (malformed %d)", len(got), len(values), w.Malformed.Load())
			}
		})
	}
}

// A record written before schema_version existed is read as version 1 while
// such records can still be in the topic. It does not match the current
// record schema, so it lives here and not among the schema's examples.
func TestReadsRecordWithoutSchemaVersion(t *testing.T) {
	w := &Service{Log: quiet}
	legacy := []byte(`{"event_id": "0192f6a8-7c1e-7b3a-9c4d-2f1e8a6b5c3d", "event_time": "2026-10-24T20:15:03.123Z",
		"tenant_id": "396438053259838069", "metric": "api_calls", "quantity": 1,
		"attributes": {"method": "GET", "path": "/items/42", "status": 200}}`)
	got := w.decode(context.Background(), records(legacy))
	if len(got) != 1 || got[0].ID != "0192f6a8-7c1e-7b3a-9c4d-2f1e8a6b5c3d" {
		t.Fatalf("decode = %+v, malformed %d", got, w.Malformed.Load())
	}
}

// A schema version newer than this writer is counted as malformed and skipped,
// not stored half-understood (HAMSP-30 turns the skip into a DLQ).
func TestSkipsNewerVersion(t *testing.T) {
	w := &Service{Log: quiet}
	newer := []byte(`{"schema_version": 99, "event_id": "0192f6a8-7c1e-7b3a-9c4d-2f1e8a6b5c3d",
		"event_time": "2026-10-24T20:15:03.123Z", "tenant_id": "396438053259838069", "metric": "api_calls", "quantity": 1}`)
	if got := w.decode(context.Background(), records(newer)); len(got) != 0 || w.Malformed.Load() != 1 {
		t.Fatalf("decode = %+v, malformed %d; want skipped", got, w.Malformed.Load())
	}
}
