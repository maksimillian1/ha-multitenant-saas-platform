package kafka

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

// AC-5 (integration): the producer waits for all in-sync replicas and is idempotent.
func TestProducerRequiresAllISRAndIdempotence(t *testing.T) {
	p, err := NewProducer([]string{"127.0.0.1:1"}, "usage-events")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got := p.client.OptValue(kgo.RequiredAcks); got != kgo.AllISRAcks() {
		t.Fatalf("RequiredAcks = %v, want all ISR", got)
	}
	if got := p.client.OptValue(kgo.DisableIdempotentWrite); got != false {
		t.Fatalf("DisableIdempotentWrite = %v, want false", got)
	}
}
