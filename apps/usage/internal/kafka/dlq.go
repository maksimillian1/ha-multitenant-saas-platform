package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Headers of a record in usage-events-dlq (HAMSP-30, feature usage-writer,
// docs/contracts/usage-event). The key and value stay the original bytes, so
// writer replay can put the record back on usage-events unchanged.
const (
	HeaderDLQReason            = "dlq-reason"             // why the writer could not store it
	HeaderDLQSource            = "dlq-source"             // topic/partition/offset it was read from
	HeaderDLQTime              = "dlq-time"               // when it was sent to the DLQ, RFC 3339
	HeaderDLQOriginalTimestamp = "dlq-original-timestamp" // the original record's Kafka timestamp, RFC 3339
	HeaderReplayedFrom         = "replayed-from"          // set by writer replay on usage-events
)

// Poison is a record the writer cannot store, and why.
type Poison struct {
	Record *kgo.Record
	Reason string
}

// DLQ produces poison records to the dead-letter topic. Like Producer it waits
// for all in-sync replicas and is idempotent: the writer commits a record's
// offset only after Send returned nil, so a DLQ write must not be lost.
type DLQ struct {
	client *kgo.Client
}

func NewDLQ(brokers []string, topic string, extra ...kgo.Opt) (*DLQ, error) {
	opts := append([]kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordDeliveryTimeout(10 * time.Second),
	}, extra...)
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &DLQ{client: client}, nil
}

func (d *DLQ) Close() { d.client.Close() }

// Send writes every poison record and returns the first error. On an error
// some records may be in the DLQ already; sending them again duplicates them
// in the DLQ, which replay turns into a no-op through the dedup key.
func (d *DLQ) Send(ctx context.Context, poison []Poison) error {
	if len(poison) == 0 {
		return nil
	}
	now := time.Now()
	records := make([]*kgo.Record, len(poison))
	for i, p := range poison {
		records[i] = DLQRecord(p.Record, p.Reason, now)
	}
	return d.client.ProduceSync(ctx, records...).FirstErr()
}

// DLQRecord is the DLQ copy of src: the same key, value and headers, plus the
// DLQ headers. Its timestamp is now, not src's, so the DLQ retention counts
// from the DLQ write; src's timestamp goes into a header.
func DLQRecord(src *kgo.Record, reason string, now time.Time) *kgo.Record {
	headers := make([]kgo.RecordHeader, 0, len(src.Headers)+4)
	headers = append(headers, src.Headers...)
	headers = append(headers,
		kgo.RecordHeader{Key: HeaderDLQReason, Value: []byte(reason)},
		kgo.RecordHeader{Key: HeaderDLQSource, Value: fmt.Appendf(nil, "%s/%d/%d", src.Topic, src.Partition, src.Offset)},
		kgo.RecordHeader{Key: HeaderDLQTime, Value: []byte(now.UTC().Format(time.RFC3339Nano))},
		kgo.RecordHeader{Key: HeaderDLQOriginalTimestamp, Value: []byte(src.Timestamp.UTC().Format(time.RFC3339Nano))},
	)
	return &kgo.Record{Key: src.Key, Value: src.Value, Headers: headers, Timestamp: now}
}
