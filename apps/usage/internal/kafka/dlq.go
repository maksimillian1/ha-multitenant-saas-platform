package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Key and value stay the original bytes, so replay puts the record back unchanged.
const (
	HeaderDLQReason            = "dlq-reason"
	HeaderDLQSource            = "dlq-source"             // topic/partition/offset
	HeaderDLQTime              = "dlq-time"               // RFC 3339
	HeaderDLQOriginalTimestamp = "dlq-original-timestamp" // RFC 3339
	HeaderReplayedFrom         = "replayed-from"
)

type Poison struct {
	Record *kgo.Record
	Reason string
}

// The writer commits an offset only after Send returned nil, so a DLQ write must not be lost.
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

// On an error some records may already be in the DLQ; a resend duplicates them, which the dedup key absorbs.
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

// The DLQ copy gets a new timestamp so retention counts from the DLQ write.
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
