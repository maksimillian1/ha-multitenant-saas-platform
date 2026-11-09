package zitadelroles

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const zitadelApp = "../../prototype/k8s/apps/zitadel"

// custom are our roles in the values file and the only permissions each may
// have.
var custom = map[string][]string{
	"IAM_BRANDING_MANAGER": {"iam.policy.delete", "iam.policy.read", "iam.policy.write"},
}

type mapping struct {
	Role        string   `yaml:"Role"`
	Permissions []string `yaml:"Permissions"`
}

type authz struct {
	InternalAuthZ struct {
		RolePermissionMappings []mapping `yaml:"RolePermissionMappings"`
	} `yaml:"InternalAuthZ"`
}

func readYAML(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: fixed paths inside the repository.
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// chartVersion is the version of the zitadel dependency of the umbrella chart.
func chartVersion(t *testing.T) string {
	t.Helper()
	var chart struct {
		Dependencies []struct {
			Name    string `yaml:"name"`
			Version string `yaml:"version"`
		} `yaml:"dependencies"`
	}
	readYAML(t, filepath.Join(zitadelApp, "Chart.yaml"), &chart)
	for _, d := range chart.Dependencies {
		if d.Name == "zitadel" {
			return d.Version
		}
	}
	t.Fatal("Chart.yaml has no zitadel dependency")
	return ""
}

func describe(m mapping) string {
	p := slices.Clone(m.Permissions)
	slices.Sort(p)
	return fmt.Sprintf("%s: %s", m.Role, strings.Join(p, ","))
}

// Verifies: admin-zitadel AC-15
func TestRoleMappingsMatchUpstream(t *testing.T) {
	version := chartVersion(t)
	upstreamFile := filepath.Join("testdata", "chart-"+version+".yaml")
	if _, err := os.Stat(upstreamFile); err != nil {
		t.Fatalf("no copy of Zitadel's default role mappings for chart %s (%v); see doc.go", version, err)
	}
	var upstream authz
	readYAML(t, upstreamFile, &upstream)
	var values struct {
		Zitadel struct {
			Zitadel struct {
				ConfigmapConfig authz `yaml:"configmapConfig"`
			} `yaml:"zitadel"`
		} `yaml:"zitadel"`
	}
	readYAML(t, filepath.Join(zitadelApp, "values.yaml"), &values)
	ours := values.Zitadel.Zitadel.ConfigmapConfig.InternalAuthZ.RolePermissionMappings

	want := upstream.InternalAuthZ.RolePermissionMappings
	if len(want) == 0 {
		t.Fatalf("%s has no role mappings", upstreamFile)
	}
	var got []mapping
	seen := map[string]bool{}
	for _, m := range ours {
		if seen[m.Role] {
			t.Errorf("role %s is mapped twice", m.Role)
		}
		seen[m.Role] = true
		allowed, isCustom := custom[m.Role]
		if !isCustom {
			got = append(got, m)
			continue
		}
		p := slices.Clone(m.Permissions)
		slices.Sort(p)
		if !slices.Equal(p, allowed) {
			t.Errorf("custom role %s has permissions %v, want exactly %v", m.Role, p, allowed)
		}
	}
	for role := range custom {
		if !seen[role] {
			t.Errorf("custom role %s is missing from values.yaml", role)
		}
	}
	if len(got) != len(want) {
		t.Errorf("values.yaml maps %d of Zitadel's roles, the defaults of chart %s have %d", len(got), version, len(want))
	}
	for i := range min(len(got), len(want)) {
		if g, w := describe(got[i]), describe(want[i]); g != w {
			t.Errorf("role mapping %d differs from Zitadel's default:\n got  %s\n want %s", i, g, w)
		}
	}
}
