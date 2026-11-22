// Package writer is Usage Writer: it consumes the usage
// topic and stores every event once (ADR-0007). It commits a batch's offsets
// only after the database transaction committed and every poison record is in
// the DLQ (HAMSP-30), so a crash in between makes Kafka deliver the batch
// again, and the store's dedup key turns the replay into a no-op.
//
// A poison record is one that can never be stored: it does not decode, it
// breaks the contract, or the database rejects it as bad data. It goes to the
// DLQ instead of being skipped or blocking the partition. Only a transient
// error, such as a database that cannot be reached, stops the partition.
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

// DeadLetters is the DLQ topic (kafka.DLQ). Send returns nil only once Kafka
// confirmed every record.
type DeadLetters interface {
	Send(ctx context.Context, poison []kafka.Poison) error
}

type Service struct {
	Client *kgo.Client
	Store  core.EventWriter
	DLQ    DeadLetters
	Log    *slog.Logger

	// afterStore runs between the database commit and the offset commit.
	// Tests use it to simulate a crash there.
	afterStore func() error

	Stored       atomic.Int64 // new events stored
	Skipped      atomic.Int64 // duplicates
	Malformed    atomic.Int64 // records that are not a valid event
	DeadLettered atomic.Int64 // records sent to the DLQ
}

// NewService takes a client from kafka.NewConsumer and a kafka.DLQ.
func NewService(client *kgo.Client, store core.EventWriter, dlq DeadLetters, log *slog.Logger) *Service {
	return &Service{Client: client, Store: store, DLQ: dlq, Log: log}
}

// pending is a decoded, valid event and the record it came from.
type pending struct {
	event  core.Event
	record *kgo.Record
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
		events, poison := w.decode(ctx, records)
		bad, ok := w.storeWithRetry(ctx, events)
		if !ok {
			return nil // shutting down; nothing committed, the batch is delivered again
		}
		poison = append(poison, bad...)
		if !w.deadLetterWithRetry(ctx, poison) {
			return nil // as above: the batch, poison included, is delivered again
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

// decode splits records into events to store and poison for the DLQ. A record
// that does not decode, including a schema version newer than this writer,
// or that breaks the contract (core.Event.ValidateContent) is poison.
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

// deadLetterWithRetry retries until every poison record is in the DLQ or ctx
// is done. While the DLQ cannot be written nothing is committed, so the
// records wait in the usage topic.
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

// storeWithRetry stores events and returns the ones the database rejects as
// bad data (core.ErrBadData). A batch with such an event is split in halves
// until the event is alone, so the others are stored and only it goes to the
// DLQ. Any other error is retried until it passes or ctx is done: while the
// database is down nothing is committed, so the events wait in Kafka.
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
