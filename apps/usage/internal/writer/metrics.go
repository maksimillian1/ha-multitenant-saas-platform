package writer

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// MetricsHandler serves the writer's counters in the Prometheus text format.
// The alert on usage_writer_records_dead_lettered_total is in
// prototype/monitoring/usage-writer.rules.yaml (HAMSP-30, AC-6).
func (w *Service) MetricsHandler(rw http.ResponseWriter, _ *http.Request) {
	rw.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	for _, m := range []struct {
		name, help string
		v          *atomic.Int64
	}{
		{"usage_writer_events_stored_total", "New usage events stored.", &w.Stored},
		{"usage_writer_events_duplicate_total", "Usage events already stored, dropped by the dedup key.", &w.Skipped},
		{"usage_writer_records_malformed_total", "Records that do not decode or break the contract.", &w.Malformed},
		{"usage_writer_records_dead_lettered_total", "Records written to the DLQ topic.", &w.DeadLettered},
	} {
		_, _ = fmt.Fprintf(rw, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", m.name, m.help, m.name, m.name, m.v.Load())
	}
}
