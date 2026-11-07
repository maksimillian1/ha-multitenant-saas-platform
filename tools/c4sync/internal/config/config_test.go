package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c4sync.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// AC (HAMSP-74): an unknown key fails with an error naming the line.
// Verifies: c4-from-code AC-3
func TestUnknownKeyNamesLine(t *testing.T) {
	p := write(t, "name: x\noutputs:\n  code: a.dsl\n")
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "outputs") {
		t.Fatalf("want error on line 2 about outputs, got %v", err)
	}
}

// AC (HAMSP-74, HAMSP-77): metadata for a component that go-arch-lint does
// not define fails with the file and line.
// Verifies: c4-from-code AC-3
func TestUnknownComponentNamesLine(t *testing.T) {
	p := write(t, "name: x\ncomponents:\n  app:\n    name: App\n  ghost:\n    name: Ghost\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	err = c.CheckComponents(map[string]bool{"app": true})
	if err == nil || !strings.Contains(err.Error(), p+":5:") || !strings.Contains(err.Error(), `"ghost"`) {
		t.Fatalf("want %s:5 about ghost, got %v", p, err)
	}
	if err := c.CheckComponents(map[string]bool{"app": true, "ghost": true}); err != nil {
		t.Fatal(err)
	}
}

func TestMatcherNeedsCallOrField(t *testing.T) {
	p := write(t, "detectors:\n  topics:\n    produce:\n      - arg: 1\n")
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "exactly one of call and field") {
		t.Fatalf("got %v", err)
	}
}

func TestDefaultsAndPaths(t *testing.T) {
	p := write(t, "projectPath: ../mod\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(p)
	if c.Project() != filepath.Join(dir, "../mod") || c.Arch() != filepath.Join(dir, "../mod/.go-arch-lint.yml") {
		t.Fatalf("project %s arch %s", c.Project(), c.Arch())
	}
	if c.IdentifierPrefix != "c" || c.Code.MaxElements != 30 || c.Name != "mod" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestSplitSymbol(t *testing.T) {
	for in, want := range map[string][2]string{
		"net/http.ServeMux.Handle":                      {"net/http", "ServeMux.Handle"},
		"fmt.Sprintf":                                   {"fmt", "Sprintf"},
		"github.com/jackc/pgx/v5.*.Exec":                {"github.com/jackc/pgx/v5", "*.Exec"},
		"github.com/twmb/franz-go/pkg/kgo.Record.Topic": {"github.com/twmb/franz-go/pkg/kgo", "Record.Topic"},
	} {
		pkg, rest, err := SplitSymbol(in)
		if err != nil || pkg != want[0] || rest != want[1] {
			t.Errorf("%s: got %q %q %v", in, pkg, rest, err)
		}
	}
	if _, _, err := SplitSymbol("nodot"); err == nil {
		t.Error("want error")
	}
}

// An empty file is an error that says so, not a bare EOF.
// Verifies: c4-from-code AC-3
func TestEmptyFile(t *testing.T) {
	_, err := Load(write(t, ""))
	if err == nil || !strings.Contains(err.Error(), "the file is empty") {
		t.Fatalf("got %v", err)
	}
}
