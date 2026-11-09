package core

import "testing"

func TestNewIDIsATenantID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := NewID()
		if !IDRe.MatchString(id) || seen[id] {
			t.Fatalf("id %q (seen before: %v)", id, seen[id])
		}
		seen[id] = true
		if len(Namespace(id)) > 63 {
			t.Fatalf("namespace %q is longer than a DNS label", Namespace(id))
		}
	}
}
