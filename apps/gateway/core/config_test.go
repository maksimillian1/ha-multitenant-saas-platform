package core

import (
	"slices"
	"testing"
)

// HAMSP-24: the Gateway does not start without a tenant list, so a missing
// ConfigMap never means "every organization is a tenant".
// Verifies: gateway-auth AC-4; T-01
func TestTenantsRequired(t *testing.T) {
	t.Setenv("ISSUER", "http://zitadel.test")
	t.Setenv("AUDIENCE", "platform-api")
	for _, tenants := range []string{"", " ", ",", " , "} {
		t.Setenv("TENANTS", tenants)
		if err := LoadConfig().Validate(); err == nil {
			t.Errorf("TENANTS=%q: Validate accepted it", tenants)
		}
	}

	t.Setenv("TENANTS", " 111 ,222,, 333 ")
	cfg := LoadConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"111", "222", "333"}; !slices.Equal(cfg.Tenants, want) {
		t.Fatalf("Tenants = %q, want %q", cfg.Tenants, want)
	}
}
