// Package writer is Usage Writer: it consumes the usage
// topic and stores every event once (ADR-0007). It commits a batch's offsets
// only after the database transaction committed, so a crash in between makes
// Kafka deliver the batch again, and the store's dedup key turns the replay
// into a no-op.
package writer

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/core"
)

const (
	maxPoll    = 500
	minBackoff = 100 * time.Millisecond
	maxBackoff = 5 * time.Second
)

type Service struct {
	Client *kgo.Client
	Store  core.EventWriter
	Log    *slog.Logger

	// afterStore runs between the database commit and the offset commit.
	// Tests use it to simulate a crash there.
	afterStore func() error

	Stored    atomic.Int64 // new events stored
	Skipped   atomic.Int64 // duplicates
	Malformed atomic.Int64 // records that are not a valid event
}

// NewService takes a client from kafka.NewConsumer.
func NewService(client *kgo.Client, store core.EventWriter, log *slog.Logger) *Service {
	return &Service{Client: client, Store: store, Log: log}
}

// Run consumes until ctx is done or the afterStore hook fails.
func (w *Service) Run(ctx context.Context) error {
	for {
		fetches := w.Client.PollRecords(ctx, maxPoll)
		if ctx.Err() != nil {
			return nil
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			w.Log.WarnContext(ctx, "fetch failed", "topic", topic, "partition", partition, "err", err)
		})
		records := fetches.Records()
		if len(records) == 0 {
			continue
		}
		events := w.decode(ctx, records)
		if !w.storeWithRetry(ctx, events) {
			return nil // shutting down; nothing committed, the batch is delivered again
		}
		if w.afterStore != nil {
			if err := w.afterStore(); err != nil {
				return err
			}
		}
		// A failed commit is not data loss: the batch is delivered again and
		// stored as duplicates, which the dedup key drops.
		if err := w.Client.CommitRecords(ctx, records...); err != nil && ctx.Err() == nil {
			w.Log.WarnContext(ctx, "offset commit failed", "records", len(records), "err", err)
		}
	}
}

func (w *Service) decode(ctx context.Context, records []*kgo.Record) []core.Event {
	events := make([]core.Event, 0, len(records))
	for _, r := range records {
		var e core.Event
		// UsageWriteAPI validated the event before it reached Kafka. A record
		// that still does not parse would block the partition forever, so it is
		// logged and skipped.
		if err := json.Unmarshal(r.Value, &e); err != nil || e.ID == "" || e.TenantID == "" || e.Time.IsZero() {
			w.Malformed.Add(1)
			w.Log.ErrorContext(ctx, "malformed usage record skipped",
				"partition", r.Partition, "offset", r.Offset, "err", err)
			continue
		}
		events = append(events, e)
	}
	return events
}

// storeWithRetry retries until the batch is stored or ctx is done. While the
// database is down nothing is committed, so the events wait in Kafka.
func (w *Service) storeWithRetry(ctx context.Context, events []core.Event) bool {
	if len(events) == 0 {
		return true
	}
	backoff := minBackoff
	for {
		fresh, err := w.Store.Write(ctx, events)
		if err == nil {
			w.Stored.Add(int64(len(fresh)))
			w.Skipped.Add(int64(len(events) - len(fresh)))
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		w.Log.WarnContext(ctx, "storing batch failed, retrying", "events", len(events), "retry_in", backoff, "err", err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}
