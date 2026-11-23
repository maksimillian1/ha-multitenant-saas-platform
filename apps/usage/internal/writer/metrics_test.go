package writer

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The rule itself is tested with promtool.
// Verifies: usage-writer AC-6
func TestDLQCounterAndAlertRule(t *testing.T) {
	w := &Service{Log: quiet}
	w.DeadLettered.Add(3)
	w.Stored.Add(10)

	rec := httptest.NewRecorder()
	w.MetricsHandler(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("Content-Type %q, want the Prometheus text format", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE usage_writer_records_dead_lettered_total counter\nusage_writer_records_dead_lettered_total 3\n",
		"# TYPE usage_writer_events_stored_total counter\nusage_writer_events_stored_total 10\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics are missing %q:\n%s", want, body)
		}
	}
}
