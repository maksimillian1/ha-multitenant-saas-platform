// Package testutil has helpers shared by the tests.
package testutil

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Root is the c4sync module root.
func Root(t testing.TB) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// Fixture is the absolute path of testdata/fixture.
func Fixture(t testing.TB) string { return filepath.Join(Root(t), "testdata", "fixture") }

// GoArchLint returns the pinned go-arch-lint binary: $C4SYNC_GO_ARCH_LINT, or
// the tool from this module's go.mod.
func GoArchLint(t testing.TB) string {
	if p := os.Getenv("C4SYNC_GO_ARCH_LINT"); p != "" {
		return p
	}
	cmd := exec.Command("go", "tool", "-n", "go-arch-lint")
	cmd.Dir = Root(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go tool -n go-arch-lint: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// CopyFixture copies testdata/fixture to a temporary folder and returns it.
func CopyFixture(t testing.TB) string {
	src, dst := Fixture(t), t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// Write writes a file, creating folders.
func Write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Replace replaces old with new in a file, failing when old is missing.
func Replace(t testing.TB, path, old, new string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), old) {
		t.Fatalf("%s: %q not found", path, old)
	}
	Write(t, path, strings.Replace(string(raw), old, new, 1))
}
