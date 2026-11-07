package dsl

import (
	"strings"
	"testing"
)

// Structurizr compares identifiers without regard to case, so Go names that
// differ only in case must not give identifiers that are equal when folded.
//
// Verifies: c4-from-code AC-11
func TestCaseSafeIdentifiers(t *testing.T) {
	pairs := [][2]string{
		{"internal/admin.humanUpdate", "internal/admin.HumanUpdate"},
		{"internal/api.ID", "internal/api.Id"},
		{"internal/api.id", "internal/api.ID"},
	}
	for _, p := range pairs {
		a, b := elemIdent(p[0]), elemIdent(p[1])
		if strings.EqualFold(a, b) {
			t.Errorf("elemIdent(%q) = %q and elemIdent(%q) = %q clash in Structurizr", p[0], a, p[1], b)
		}
		ca, cb := compIdent("hamsp", p[0]), compIdent("hamsp", p[1])
		if strings.EqualFold(ca, cb) {
			t.Errorf("compIdent: %q and %q clash", ca, cb)
		}
	}
	// A name without upper-case letters keeps its spelling.
	if got := elemIdent("internal/app.store"); got != "t_internal_app_store" {
		t.Errorf("elemIdent(internal/app.store) = %q", got)
	}
}
