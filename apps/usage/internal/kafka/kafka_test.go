package kafka

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
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

// HAMSP-31: each binary authenticates with SCRAM-SHA-512 when it has a username.
func TestSCRAM(t *testing.T) {
	opts, err := SCRAM("", "")
	if err != nil || opts != nil {
		t.Fatalf("no username: opts=%v err=%v, want none", opts, err)
	}
	if _, err := SCRAM("usage-writer", ""); err == nil {
		t.Fatal("username without password: want error")
	}
	opts, err = SCRAM("usage-writer", "secret") // gitleaks:allow
	if err != nil || len(opts) != 1 {
		t.Fatalf("opts=%v err=%v, want one SASL option", opts, err)
	}
	c, err := kgo.NewClient(append(opts, kgo.SeedBrokers("127.0.0.1:1"))...)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	mechs, _ := c.OptValue(kgo.SASL).([]sasl.Mechanism)
	if len(mechs) != 1 || mechs[0].Name() != "SCRAM-SHA-512" {
		t.Fatalf("SASL = %v, want SCRAM-SHA-512", mechs)
	}
}
