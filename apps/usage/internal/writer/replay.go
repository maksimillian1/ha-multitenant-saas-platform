package writer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/kafka"
)

// ReplaySpec chooses DLQ records: offsets From to To, both included, of one
// partition of the DLQ topic.
type ReplaySpec struct {
	Partition int32
	From, To  int64
}

// Replay is writer replay (HAMSP-30): it puts the chosen DLQ records back on
// the usage topic with the same key and value, the original headers and
// replayed-from=dlq, and returns how many it wrote. It runs as the KafkaUser
// usage-replay, which may read only the DLQ and write only the usage topic.
//
// Replaying is safe to repeat: the writer's dedup key (tenant_id, event_id,
// event_time) stores an event once however often it arrives (ADR-0007). A
// record that is still poison goes back to the DLQ. Replay is not for events
// of a closed billing period: that rule is open (HAMSP-30, Open questions).
func Replay(ctx context.Context, brokers []string, dlqTopic, usageTopic string, spec ReplaySpec, extra ...kgo.Opt) (int, error) {
	if spec.From < 0 || spec.To < spec.From {
		return 0, fmt.Errorf("offsets %d to %d: want 0 <= from <= to", spec.From, spec.To)
	}
	reader, err := kgo.NewClient(append([]kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
			dlqTopic: {spec.Partition: kgo.NewOffset().At(spec.From)},
		}),
	}, extra...)...)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	writer, err := kgo.NewClient(append([]kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(usageTopic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordDeliveryTimeout(10 * time.Second),
	}, extra...)...)
	if err != nil {
		return 0, err
	}
	defer writer.Close()

	var out []*kgo.Record
	for done := false; !done; {
		fetches := reader.PollFetches(ctx)
		if ctx.Err() != nil {
			return 0, fmt.Errorf("read %d records of %s/%d up to offset %d: %w", len(out), dlqTopic, spec.Partition, spec.To, ctx.Err())
		}
		if err := fetches.Err(); err != nil {
			return 0, err
		}
		for _, r := range fetches.Records() {
			if r.Offset > spec.To {
				done = true
				break
			}
			out = append(out, replayRecord(r))
			if r.Offset == spec.To {
				done = true
				break
			}
		}
	}
	if err := writer.ProduceSync(ctx, out...).FirstErr(); err != nil {
		return 0, err
	}
	return len(out), nil
}

// replayRecord is the usage topic copy of a DLQ record: the DLQ headers are
// dropped, replayed-from is added.
func replayRecord(r *kgo.Record) *kgo.Record {
	headers := make([]kgo.RecordHeader, 0, len(r.Headers)+1)
	for _, h := range r.Headers {
		if !strings.HasPrefix(h.Key, "dlq-") {
			headers = append(headers, h)
		}
	}
	headers = append(headers, kgo.RecordHeader{Key: kafka.HeaderReplayedFrom, Value: []byte("dlq")})
	return &kgo.Record{Key: r.Key, Value: r.Value, Headers: headers}
}
