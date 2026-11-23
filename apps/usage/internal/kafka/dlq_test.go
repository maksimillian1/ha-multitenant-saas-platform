package kafka

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
)

func header(r *kgo.Record, key string) (string, bool) {
	for _, h := range r.Headers {
		if h.Key == key {
			return string(h.Value), true
		}
	}
	return "", false
}

func poisonRecord() *kgo.Record {
	return &kgo.Record{
		Topic:     "usage-events",
		Partition: 3,
		Offset:    12345,
		Key:       []byte("tenant-a"),
		Value:     []byte(`{"schema_version":1,"event_id":"e1","quantity":0}` + "\x00not json"),
		Timestamp: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
		Headers:   []kgo.RecordHeader{{Key: "traceparent", Value: []byte("00-abc-def-01")}},
	}
}

// Verifies: usage-writer AC-1; T-02
func TestDLQRecordKeepsBytesAndAddsHeaders(t *testing.T) {
	src := poisonRecord()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	got := DLQRecord(src, "quantity must be positive", now)

	if got.Topic != "" {
		t.Fatalf("Topic = %q, want empty so the DLQ producer's default topic is used", got.Topic)
	}
	if !bytes.Equal(got.Key, src.Key) || !bytes.Equal(got.Value, src.Value) {
		t.Fatalf("key/value changed: %q %q", got.Key, got.Value)
	}
	if !got.Timestamp.Equal(now) {
		t.Fatalf("Timestamp = %v, want the DLQ write time %v, so retention counts from the DLQ write", got.Timestamp, now)
	}
	want := map[string]string{
		HeaderDLQReason:            "quantity must be positive",
		HeaderDLQSource:            "usage-events/3/12345",
		HeaderDLQTime:              "2026-11-22T12:00:00Z",
		HeaderDLQOriginalTimestamp: "2026-11-01T10:00:00Z",
		"traceparent":              "00-abc-def-01",
	}
	for k, v := range want {
		if h, ok := header(got, k); !ok || h != v {
			t.Errorf("header %s = %q (present %v), want %q", k, h, ok, v)
		}
	}
	if len(src.Headers) != 1 {
		t.Fatalf("the source record's headers were changed: %v", src.Headers)
	}
}

func TestDLQProducerRequiresAllISRAndIdempotence(t *testing.T) {
	d, err := NewDLQ([]string{"127.0.0.1:1"}, "usage-events-dlq")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got := d.client.OptValue(kgo.RequiredAcks); got != kgo.AllISRAcks() {
		t.Fatalf("RequiredAcks = %v, want all ISR", got)
	}
	if got := d.client.OptValue(kgo.DisableIdempotentWrite); got != false {
		t.Fatalf("DisableIdempotentWrite = %v, want false", got)
	}
}

// Verifies: usage-writer AC-1
func TestDLQSendWritesToDLQTopic(t *testing.T) {
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(6, "usage-events-dlq"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	d, err := NewDLQ(c.ListenAddrs(), "usage-events-dlq")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	src := poisonRecord()
	before := time.Now().Add(-time.Second)
	if err := d.Send(ctx, []Poison{{Record: src, Reason: "bad json"}}); err != nil {
		t.Fatal(err)
	}

	reader, err := kgo.NewClient(kgo.SeedBrokers(c.ListenAddrs()...), kgo.ConsumeTopics("usage-events-dlq"),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	fetches := reader.PollFetches(ctx)
	if err := fetches.Err(); err != nil {
		t.Fatal(err)
	}
	recs := fetches.Records()
	if len(recs) != 1 {
		t.Fatalf("got %d records in the DLQ, want 1", len(recs))
	}
	r := recs[0]
	if !bytes.Equal(r.Value, src.Value) || !bytes.Equal(r.Key, src.Key) {
		t.Fatalf("stored key/value differ from the original")
	}
	if r.Timestamp.Before(before) {
		t.Fatalf("stored Timestamp %v is the original's, want the DLQ write time", r.Timestamp)
	}
	if reason, _ := header(r, HeaderDLQReason); reason != "bad json" {
		t.Fatalf("%s = %q", HeaderDLQReason, reason)
	}
}

func TestDLQSendFailsWhenKafkaDoesNotConfirm(t *testing.T) {
	d, err := NewDLQ([]string{"127.0.0.1:1"}, "usage-events-dlq", kgo.RecordDeliveryTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Send(ctx, []Poison{{Record: poisonRecord(), Reason: "x"}}); err == nil {
		t.Fatal("Send returned nil without a broker; the writer would commit the offset and lose the record")
	}
}
