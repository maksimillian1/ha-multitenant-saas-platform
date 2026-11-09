package api

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
)

// The Tenant Management contract (docs/contracts/tenants).
const examples = "../../../docs/contracts/tenants/examples/"

// shape is the JSON kind of every field, objects recursively, so a renamed,
// missing, extra or retyped field changes it while the values do not matter.
func shape(v any) any {
	switch v := v.(type) {
	case map[string]any:
		s := map[string]any{}
		for k, x := range v {
			s[k] = shape(x)
		}
		return s
	case []any:
		if len(v) == 0 {
			return "array"
		}
		return []any{shape(v[0])}
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	default:
		return "null"
	}
}

func shapeOf(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return shape(v)
}

func example(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(examples + name) //nolint:gosec // G304: the contract's example files, by names fixed in this test.
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Verifies: tenant-management AC-1, AC-7; T-02
func TestContractExamples(t *testing.T) {
	e := newEnv(t)

	created := e.do(t, http.MethodPost, "/tenants", tokenManager, string(example(t, "create-tenant.json")))
	if created.status != http.StatusCreated {
		t.Fatalf("the example create request: %d %s", created.status, created.body)
	}
	if got, want := shapeOf(t, created.body), shapeOf(t, example(t, "tenant-active.json")); !reflect.DeepEqual(got, want) {
		t.Fatalf("active tenant shape:\n got %v\nwant %v", got, want)
	}
	id := created.tenant(t).ID

	e.deps.FailNext["DeactivateOrganization"] = 1
	failed := e.do(t, http.MethodPost, "/tenants/"+id+"/suspend", tokenManager, "")
	if got, want := shapeOf(t, failed.body), shapeOf(t, example(t, "error-step-failed.json")); !reflect.DeepEqual(got, want) {
		t.Fatalf("step error shape:\n got %v\nwant %v", got, want)
	}

	if r := e.do(t, http.MethodPost, "/tenants/"+id+"/suspend", tokenManager, ""); r.status != http.StatusOK {
		t.Fatalf("suspend: %d %s", r.status, r.body)
	}
	deleted := e.do(t, http.MethodDelete, "/tenants/"+id, tokenManager, "")
	if got, want := shapeOf(t, deleted.body), shapeOf(t, example(t, "tenant-deleted.json")); !reflect.DeepEqual(got, want) {
		t.Fatalf("tombstone shape:\n got %v\nwant %v", got, want)
	}
}
