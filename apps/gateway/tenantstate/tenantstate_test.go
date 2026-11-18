package tenantstate

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
)

const topic = "tenant-states"

func publish(t *testing.T, c *kgo.Client, tenant, state string) {
	t.Helper()
	v, _ := json.Marshal(map[string]any{"tenant_id": tenant, "state": state, "at": time.Now()})
	if err := c.ProduceSync(context.Background(), &kgo.Record{Topic: topic, Key: []byte(tenant), Value: v}).FirstErr(); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal(what)
}

// Verifies: gateway-auth AC-2
func TestSuspendedTenantIsBlocked(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(3, topic))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Close)
	producer, err := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)

	// Published before the Gateway starts: it reads the topic from the start.
	publish(t, producer, "a", "active")
	publish(t, producer, "b", "suspended")

	states := New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = states.Follow(ctx, cluster.ListenAddrs(), topic, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	eventually(t, "b is not blocked", func() bool { return states.Blocked("b") })
	if states.Blocked("a") || states.Blocked("unknown") {
		t.Fatal("an active or unknown tenant is blocked")
	}

	publish(t, producer, "a", "suspended")
	eventually(t, "a is not blocked after suspend", func() bool { return states.Blocked("a") })
	publish(t, producer, "b", "active")
	eventually(t, "b is still blocked after resume", func() bool { return !states.Blocked("b") })
	for _, s := range []string{"deleting", "deleted"} {
		publish(t, producer, "c-"+s, s)
		eventually(t, s+" tenant is not blocked", func() bool { return states.Blocked("c-" + s) })
	}

	// Kafka goes away: the last state read stays.
	cluster.Close()
	time.Sleep(100 * time.Millisecond)
	if !states.Blocked("a") || states.Blocked("b") {
		t.Fatal("state lost when Kafka went down")
	}
}
