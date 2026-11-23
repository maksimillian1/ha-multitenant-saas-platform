package writer

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/kafka"
)

const dlqTopic = "usage-events-dlq"

func (p *pipeline) dlqRecords(want int) []*kgo.Record {
	p.t.Helper()
	c, err := kgo.NewClient(kgo.SeedBrokers(p.brokers...), kgo.ConsumeTopics(dlqTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		p.t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out []*kgo.Record
	for len(out) < want {
		f := c.PollFetches(ctx)
		if ctx.Err() != nil {
			p.t.Fatalf("%d records in %s, want %d", len(out), dlqTopic, want)
		}
		out = append(out, f.Records()...)
	}
	return out
}

func (p *pipeline) replay(r *kgo.Record) int {
	p.t.Helper()
	n, err := Replay(context.Background(), p.brokers, dlqTopic, topic, ReplaySpec{Partition: r.Partition, From: r.Offset, To: r.Offset})
	if err != nil {
		p.t.Fatal(err)
	}
	return n
}

// Verifies: usage-writer AC-7; T-01; QS-06
func TestReplayStoresOnceAndTwiceIsNoop(t *testing.T) {
	p := newPipeline(t)
	dlq, err := kafka.NewDLQ(p.brokers, dlqTopic)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dlq.Close)

	// Sent to the DLQ for a cause fixed since.
	e := newEvent("tenant-b")
	value := rawEvent(t, e, core.SchemaVersion)
	src := &kgo.Record{Topic: topic, Partition: 1, Offset: 7, Key: []byte(e.TenantID), Value: value, Timestamp: time.Now()}
	if err := dlq.Send(context.Background(), []kafka.Poison{{Record: src, Reason: "fixed since"}}); err != nil {
		t.Fatal(err)
	}
	inDLQ := p.dlqRecords(1)[0]

	p.startWriter(p.st, nil, nil)
	if n := p.replay(inDLQ); n != 1 {
		t.Fatalf("replayed %d records, want 1", n)
	}
	p.waitForCount([]core.Event{e}, 1, 20*time.Second)
	if n := p.db.CountTenant(t, "tenant-b", []string{e.ID}); n != 1 {
		t.Fatalf("the replayed event is stored %d times under tenant-b, want 1", n)
	}

	main := p.topicRecords(func(r *kgo.Record) bool { return bytes.Equal(r.Value, value) })
	if len(main) != 1 {
		t.Fatalf("%d replayed records on %s, want 1", len(main), topic)
	}
	if !bytes.Equal(main[0].Key, src.Key) {
		t.Fatalf("key %q, want %q", main[0].Key, src.Key)
	}
	if v, _ := headerOf(main[0], kafka.HeaderReplayedFrom); v != "dlq" {
		t.Fatalf("%s = %q, want dlq", kafka.HeaderReplayedFrom, v)
	}
	if _, ok := headerOf(main[0], kafka.HeaderDLQReason); ok {
		t.Fatal("the DLQ headers were copied to the usage topic")
	}

	p.replay(inDLQ)
	p.waitForCount([]core.Event{e}, 1, 5*time.Second)
}

func headerOf(r *kgo.Record, key string) (string, bool) {
	for _, h := range r.Headers {
		if h.Key == key {
			return string(h.Value), true
		}
	}
	return "", false
}

func (p *pipeline) topicRecords(keep func(*kgo.Record) bool) []*kgo.Record {
	p.t.Helper()
	c, err := kgo.NewClient(kgo.SeedBrokers(p.brokers...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		p.t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out []*kgo.Record
	for ctx.Err() == nil {
		for _, r := range c.PollFetches(ctx).Records() {
			if keep(r) {
				out = append(out, r)
			}
		}
	}
	return out
}
