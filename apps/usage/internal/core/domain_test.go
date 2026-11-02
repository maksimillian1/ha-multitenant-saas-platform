package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func valid() Event {
	return Event{
		ID: "0192f6a8-7c1e-7b3a-9c4d-2f1e8a6b5c3d", Time: now.Add(-time.Minute),
		TenantID: "396438053259838069", Metric: "api_calls", Quantity: 1,
		Attributes: json.RawMessage(`{"method":"GET","path":"/items","status":200}`),
	}
}

// AC-6: an event that breaks the contract is rejected.
func TestValidate(t *testing.T) {
	if err := valid().Validate(now); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	noAttrs := valid()
	noAttrs.Attributes = nil
	if err := noAttrs.Validate(now); err != nil {
		t.Fatalf("event without attributes rejected: %v", err)
	}

	cases := map[string]func(*Event){
		"no event_id":       func(e *Event) { e.ID = "" },
		"event_id not uuid": func(e *Event) { e.ID = "42" },
		"no event_time":     func(e *Event) { e.Time = time.Time{} },
		"too old":           func(e *Event) { e.Time = now.Add(-MaxAge - time.Second) },
		"in the future":     func(e *Event) { e.Time = now.Add(MaxClockSkew + time.Second) },
		"no tenant_id":      func(e *Event) { e.TenantID = "" },
		"bad tenant_id":     func(e *Event) { e.TenantID = "a b" },
		"no metric":         func(e *Event) { e.Metric = "" },
		"unknown metric":    func(e *Event) { e.Metric = "cpu_seconds" },
		"no quantity":       func(e *Event) { e.Quantity = 0 },
		"negative quantity": func(e *Event) { e.Quantity = -1 },
		"attributes array":  func(e *Event) { e.Attributes = json.RawMessage(`[1]`) },
		"attributes broken": func(e *Event) { e.Attributes = json.RawMessage(`{"a":`) },
		"attributes too big": func(e *Event) {
			e.Attributes = json.RawMessage(`{"a":"` + strings.Repeat("x", MaxAttributes) + `"}`)
		},
	}
	for name, mod := range cases {
		e := valid()
		mod(&e)
		if err := e.Validate(now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateBatchRejectsWholeBatch(t *testing.T) {
	bad := valid()
	bad.Metric = "nope"
	err := ValidateBatch([]Event{valid(), bad}, now)
	if err == nil || !strings.Contains(err.Error(), "event 1") {
		t.Fatalf("err = %v, want it to name event 1", err)
	}
	if ValidateBatch(nil, now) == nil {
		t.Fatal("empty batch accepted")
	}
	if ValidateBatch(make([]Event, MaxBatch+1), now) == nil {
		t.Fatal("oversized batch accepted")
	}
}
