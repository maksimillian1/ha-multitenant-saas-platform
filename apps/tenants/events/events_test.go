package events

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
)

const topic = "tenant-states"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// read returns the latest state per tenant in the topic.
func read(t *testing.T, addrs []string) map[string]string {
	t.Helper()
	c, err := kgo.NewClient(kgo.SeedBrokers(addrs...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got := map[string]string{}
	for ctx.Err() == nil {
		c.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
			var e struct {
				TenantID string    `json:"tenant_id"`
				State    string    `json:"state"`
				At       time.Time `json:"at"`
			}
			if err := json.Unmarshal(r.Value, &e); err != nil || e.TenantID != string(r.Key) || e.At.IsZero() {
				t.Errorf("bad record %q: %s, %v", r.Key, r.Value, err)
			}
			got[e.TenantID] = e.State
		})
	}
	return got
}

// Verifies: tenant-management AC-18
func TestStateChangesArePublished(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(3, topic))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Close)
	p, err := NewPublisher(cluster.ListenAddrs(), topic, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)

	p.Publish(context.Background(), core.Tenant{ID: "a", State: core.StateSuspended})
	if got := read(t, cluster.ListenAddrs()); got["a"] != "suspended" {
		t.Fatalf("after publish: %v", got)
	}

	all := []core.Tenant{{ID: "a", State: core.StateActive}, {ID: "b", State: core.StateDeleted}}
	if err := p.Resync(context.Background(), func(context.Context) ([]core.Tenant, error) { return all, nil }); err != nil {
		t.Fatal(err)
	}
	if got := read(t, cluster.ListenAddrs()); got["a"] != "active" || got["b"] != "deleted" {
		t.Fatalf("after resync: %v", got)
	}

	// Kafka is gone: Publish logs and returns, it never blocks the caller long.
	cluster.Close()
	start := time.Now()
	p.Publish(context.Background(), core.Tenant{ID: "a", State: core.StateSuspended})
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("Publish blocked %v with Kafka down", d)
	}
}
