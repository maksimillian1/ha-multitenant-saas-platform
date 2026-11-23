// Package writer commits offsets only after the DB commit and the DLQ write (ADR-0007); only a
// transient error stops a partition, poison goes to the DLQ.
package writer

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/kafka"
)

const (
	maxPoll    = 500
	minBackoff = 100 * time.Millisecond
	maxBackoff = 5 * time.Second
)

// Send returns nil only once Kafka confirmed every record.
type DeadLetters interface {
	Send(ctx context.Context, poison []kafka.Poison) error
}

type Service struct {
	Client *kgo.Client
	Store  core.EventWriter
	DLQ    DeadLetters
	Log    *slog.Logger

	// Tests use it to simulate a crash between the database commit and the offset commit.
	afterStore func() error

	Stored       atomic.Int64
	Skipped      atomic.Int64
	Malformed    atomic.Int64
	DeadLettered atomic.Int64
}

func NewService(client *kgo.Client, store core.EventWriter, dlq DeadLetters, log *slog.Logger) *Service {
	return &Service{Client: client, Store: store, DLQ: dlq, Log: log}
}

type pending struct {
	event  core.Event
	record *kgo.Record
}

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
		events, poison := w.decode(ctx, records)
		bad, ok := w.storeWithRetry(ctx, events)
		if !ok {
			return nil // nothing committed, the batch is delivered again
		}
		poison = append(poison, bad...)
		if !w.deadLetterWithRetry(ctx, poison) {
			return nil
		}
		if w.afterStore != nil {
			if err := w.afterStore(); err != nil {
				return err
			}
		}
		// A failed commit only redelivers the batch; the dedup key drops the duplicates.
		if err := w.Client.CommitRecords(ctx, records...); err != nil && ctx.Err() == nil {
			w.Log.WarnContext(ctx, "offset commit failed", "records", len(records), "err", err)
		}
	}
}

func (w *Service) decode(ctx context.Context, records []*kgo.Record) ([]pending, []kafka.Poison) {
	events := make([]pending, 0, len(records))
	var poison []kafka.Poison
	for _, r := range records {
		e, err := kafka.Decode(r.Value)
		if err == nil {
			err = e.ValidateContent()
		}
		if err != nil {
			w.Malformed.Add(1)
			w.Log.ErrorContext(ctx, "poison usage record, sending to the DLQ",
				"partition", r.Partition, "offset", r.Offset, "err", err)
			poison = append(poison, kafka.Poison{Record: r, Reason: err.Error()})
			continue
		}
		events = append(events, pending{event: e, record: r})
	}
	return events, poison
}

func eventsOf(ps []pending) []core.Event {
	out := make([]core.Event, len(ps))
	for i, p := range ps {
		out[i] = p.event
	}
	return out
}

// Nothing is committed while the DLQ is down, so the records wait in the usage topic.
func (w *Service) deadLetterWithRetry(ctx context.Context, poison []kafka.Poison) bool {
	if len(poison) == 0 {
		return true
	}
	backoff := minBackoff
	for {
		err := w.DLQ.Send(ctx, poison)
		if err == nil {
			w.DeadLettered.Add(int64(len(poison)))
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		w.Log.WarnContext(ctx, "writing to the DLQ failed, retrying", "records", len(poison), "retry_in", backoff, "err", err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// A batch with bad data is halved until the bad event is alone. Other errors are retried:
// nothing is committed, so the events wait in Kafka.
func (w *Service) storeWithRetry(ctx context.Context, events []pending) ([]kafka.Poison, bool) {
	if len(events) == 0 {
		return nil, true
	}
	backoff := minBackoff
	for {
		batch := eventsOf(events)
		fresh, err := w.Store.Write(ctx, batch)
		if err == nil {
			w.Stored.Add(int64(len(fresh)))
			w.Skipped.Add(int64(len(batch) - len(fresh)))
			return nil, true
		}
		if ctx.Err() != nil {
			return nil, false
		}
		if errors.Is(err, core.ErrBadData) {
			if len(events) == 1 {
				r := events[0].record
				w.Log.ErrorContext(ctx, "the database rejected a usage record, sending to the DLQ",
					"partition", r.Partition, "offset", r.Offset, "err", err)
				return []kafka.Poison{{Record: r, Reason: err.Error()}}, true
			}
			half := len(events) / 2
			left, ok := w.storeWithRetry(ctx, events[:half])
			if !ok {
				return nil, false
			}
			right, ok := w.storeWithRetry(ctx, events[half:])
			return append(left, right...), ok
		}
		w.Log.WarnContext(ctx, "storing batch failed, retrying", "events", len(events), "retry_in", backoff, "err", err)
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}
