package tenantstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// contractExamples are the examples of docs/contracts/tenant-states/record.v1.json.
func contractExamples(t *testing.T) []json.RawMessage {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../docs/contracts/tenant-states/record.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Examples []json.RawMessage `json:"examples"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil || len(schema.Examples) == 0 {
		t.Fatalf("record.v1.json: %v, %d examples", err, len(schema.Examples))
	}
	return schema.Examples
}

// The Gateway reads every example of the contract, in order: the latest state
// wins, suspended, deleting and deleted block, other states pass, and a state
// it does not know keeps the tenant's earlier one.
//
// Verifies: gateway-auth AC-2; tenant-management T-02
func TestContractExamples(t *testing.T) {
	s := New()
	for _, raw := range contractExamples(t) {
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("example %s: %v", raw, err)
		}
		s.apply(e)
	}
	for tenant, want := range map[string]bool{
		"396438053259838069": true,  // active, then suspended
		"396438053259838070": true,  // deleting
		"396438053259838071": true,  // deleted
		"396438053259838072": false, // provisioning
	} {
		if got := s.Blocked(tenant); got != want {
			t.Errorf("%s: blocked %v, want %v", tenant, got, want)
		}
	}
	s.apply(Event{TenantID: "396438053259838069", State: "frozen"})
	if !s.Blocked("396438053259838069") {
		t.Error("an unknown state unblocked a suspended tenant")
	}
}
