package events

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
)

type recordSchema struct {
	Required   []string `json:"required"`
	Properties struct {
		TenantID struct {
			Pattern string `json:"pattern"`
		} `json:"tenant_id"`
		State struct {
			Enum []string `json:"enum"`
		} `json:"state"`
	} `json:"properties"`
}

func loadSchema(t *testing.T) recordSchema {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../docs/contracts/tenant-states/record.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s recordSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// Every state Tenant Management has is in the contract, and every record it
// publishes has exactly the contract's fields, the key equal to tenant_id and
// an RFC 3339 time.
//
// Verifies: tenant-management T-02; AC-18
func TestRecordsMatchContract(t *testing.T) {
	schema := loadSchema(t)
	states := []core.State{core.StateProvisioning, core.StateActive, core.StateSuspended, core.StateDeleting, core.StateDeleted}
	var tenants []core.Tenant
	for i, st := range states {
		if !slices.Contains(schema.Properties.State.Enum, string(st)) {
			t.Errorf("state %q is not in the contract", st)
		}
		tenants = append(tenants, core.Tenant{ID: "39643805325983807" + string(rune('0'+i)), State: st})
	}

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
	if err := p.Resync(context.Background(), func(context.Context) ([]core.Tenant, error) { return tenants, nil }); err != nil {
		t.Fatal(err)
	}

	c, err := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pattern := regexp.MustCompile(schema.Properties.TenantID.Pattern)
	seen := 0
	for ctx.Err() == nil {
		c.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
			seen++
			var fields map[string]any
			if err := json.Unmarshal(r.Value, &fields); err != nil {
				t.Fatalf("record %s: %v", r.Value, err)
			}
			keys := make([]string, 0, len(fields))
			for k := range fields {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			want := slices.Clone(schema.Required)
			slices.Sort(want)
			if !slices.Equal(keys, want) {
				t.Errorf("record %s: fields %v, want %v", r.Value, keys, want)
			}
			id, _ := fields["tenant_id"].(string)
			if id != string(r.Key) || !pattern.MatchString(id) {
				t.Errorf("record %s: key %q", r.Value, r.Key)
			}
			if at, _ := fields["at"].(string); at == "" {
				t.Errorf("record %s: no at", r.Value)
			} else if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
				t.Errorf("record %s: at: %v", r.Value, err)
			}
		})
	}
	if seen != len(states) {
		t.Fatalf("read %d records, want %d", seen, len(states))
	}
}
