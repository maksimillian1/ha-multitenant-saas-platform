package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRepo is a repository in memory: files and the files changed since
// each commit.
type fakeRepo struct {
	files   map[string]string
	changed map[string][]string
}

func (r fakeRepo) Files() ([]string, error) {
	var out []string
	for p := range r.files {
		out = append(out, p)
	}
	return out, nil
}

func (r fakeRepo) ChangedSince(commit string) ([]string, error) { return r.changed[commit], nil }

func (r fakeRepo) Read(path string) ([]byte, error) {
	s, ok := r.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(s), nil
}

func TestGlob(t *testing.T) {
	cases := []struct {
		glob, file string
		want       bool
	}{
		{"apps/gateway/auth/**", "apps/gateway/auth/auth.go", true},
		{"apps/gateway/auth/**", "apps/gateway/auth/x/y.go", true},
		{"apps/gateway/auth/**", "apps/gateway/authz/a.go", false},
		{"apps/*/store/**", "apps/exampleapp/store/store.go", true},
		{"apps/*/store/**", "apps/a/b/store/store.go", false},
		{"**/migrations/*.sql", "apps/usage/internal/migrations/0001.sql", true},
		{"**/migrations/*.sql", "migrations/0001.sql", true},
		{"prototype/k8s/apps/*/network-policy.yaml", "prototype/k8s/apps/usage/network-policy.yaml", true},
		{"docs/contracts/gateway-headers.md", "docs/contracts/gateway-headers.md", true},
		{"a?c", "abc", true},
		{"a?c", "a/c", false},
	}
	for _, c := range cases {
		got, err := matchAny([]string{c.glob}, []string{c.file})
		if err != nil {
			t.Fatal(err)
		}
		if (len(got) == 1) != c.want {
			t.Errorf("%q on %q: got %v, want %v", c.glob, c.file, len(got) == 1, c.want)
		}
	}
}

func TestVerifies(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"// Verifies: tenant-suspend AC-2; T-01", true},
		{"// Verifies: tenant-suspend AC-1, AC-2; T-01", true},
		{"// Verifies: tenant-suspend AC-21", false},
		{"// Verifies: tenant AC-2", false},
		{"// verifies tenant-suspend AC-2", false},
	}
	for _, c := range cases {
		if got := verifies(c.line, "tenant-suspend", "AC-2"); got != c.want {
			t.Errorf("%q: got %v, want %v", c.line, got, c.want)
		}
	}
}

const testFile = `package suspend

// Verifies: tenant-suspend AC-1; T-01
func TestSuspendedTenantGets403(t *testing.T) {}

func TestNoComment(t *testing.T) {}
`

func TestCheckTest(t *testing.T) {
	repo := fakeRepo{files: map[string]string{"apps/gateway/suspend/suspend_test.go": testFile}}
	cases := []struct {
		c       claim
		wantErr string
	}{
		{claim{"AC-1", "apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403"}, ""},
		{claim{"T-01", "apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403"}, ""},
		{claim{"AC-2", "apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403"}, "no \"Verifies"},
		{claim{"AC-1", "apps/gateway/suspend/suspend_test.go:TestNoComment"}, "no \"Verifies"},
		{claim{"AC-1", "apps/gateway/suspend/suspend_test.go:TestMissing"}, "not found in"},
		{claim{"AC-1", "apps/gateway/suspend/other_test.go:TestX"}, "file not found"},
		{claim{"AC-1", "TestX"}, "must be path:TestName"},
	}
	for _, c := range cases {
		err := checkTest(repo, "tenant-suspend", c.c)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%v: unexpected error %v", c.c, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%v: got %v, want error containing %q", c.c, err, c.wantErr)
		}
	}
}

func writeFeature(t *testing.T, root, slug, body string) {
	t.Helper()
	dir := filepath.Join(root, featuresDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, slug+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name, slug, body, wantErr string
	}{
		{"name differs", "tenant-suspend", "feature: other\ntitle: X\ntarget: alpha\n", "must equal the file name"},
		{"no target", "a", "feature: a\ntitle: X\n", "target"},
		{"stable without review", "a", "feature: a\ntitle: X\ntarget: stable\nstage: stable\ncode: [x/**]\n", "reviews is empty"},
		{"stage without code", "a", "feature: a\ntitle: X\ntarget: beta\nstage: alpha\n", "needs code globs"},
		{"bad item", "a", "feature: a\ntitle: X\ntarget: alpha\nchecklist:\n  T-13:\n    planned: applies\n", "T-01 to T-12"},
		{"ac n/a", "a", "feature: a\ntitle: X\ntarget: alpha\nacceptance:\n  AC-1:\n    text: x\n    verified: n/a — no\n", "cannot be n/a"},
		{"bad verified", "a", "feature: a\ntitle: X\ntarget: alpha\nchecklist:\n  T-01:\n    verified: done\n", "list of tests"},
		{"unknown field", "a", "feature: a\ntitle: X\ntarget: alpha\nowner: me\n", "field owner not found"},
		{"bad review", "a", "feature: a\ntitle: X\ntarget: beta\nreviews:\n  - {date: 2027-01-01, commit: zz, scope: full}\n", "not a commit hash"},
		{"ok", "a", "feature: a\ntitle: X\ntarget: alpha\nplan_approved: 2026-11-12\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeFeature(t, root, c.slug, c.body)
			fs, err := loadFeatures(root, featuresDir)
			var errs []error
			if err != nil {
				errs = append(errs, err)
			}
			for _, f := range fs {
				errs = append(errs, f.validate()...)
			}
			joined := ""
			for _, e := range errs {
				joined += e.Error() + "\n"
			}
			if c.wantErr == "" && joined != "" {
				t.Fatalf("unexpected errors:\n%s", joined)
			}
			if c.wantErr != "" && !strings.Contains(joined, c.wantErr) {
				t.Fatalf("got errors:\n%s\nwant one containing %q", joined, c.wantErr)
			}
		})
	}
}

const readmeDoc = "# Features\n\nText.\n\n" + startMarker + "\nold\n" + endMarker + "\n\nMore text.\n"

func TestRun(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, featuresDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, readme), []byte(readmeDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFeature(t, root, "gateway-auth", `feature: gateway-auth
title: JWT validation | tenant context
target: stable
stage: stable
critical: true
code: ["apps/gateway/auth/**"]
links: {issues: [HAMSP-23]}
reviews:
  - {date: 2027-02-01, commit: aaaaaaa1, scope: full}
runs:
  - {date: 2027-01-20, commit: bbbbbbb2, stage_checked: stable, verdict: ready, failed: [], not_checked: [T-12]}
`)
	writeFeature(t, root, "tenant-suspend", `feature: tenant-suspend
title: Suspend a tenant
target: alpha
acceptance:
  AC-1:
    text: A suspended tenant gets 403.
    level: integration
    planned: apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403
    verified: [apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403]
  AC-2:
    text: An active tenant passes.
    level: integration
`)
	repo := fakeRepo{
		files: map[string]string{
			"apps/gateway/auth/auth.go":            "package auth",
			"apps/gateway/suspend/suspend_test.go": testFile,
		},
		changed: map[string][]string{"aaaaaaa1": {"apps/gateway/auth/auth.go", "README.md"}},
	}

	if err := run(root, repo, true); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("check before writing: got %v, want out of date", err)
	}
	if err := run(root, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := run(root, repo, true); err != nil {
		t.Fatalf("check after writing: %v", err)
	}

	doc, _ := os.ReadFile(filepath.Join(root, readme))
	got := string(doc)
	for _, want := range []string{
		"# Features\n\nText.\n\n" + startMarker + "\n| Feature |",
		"| [`gateway-auth`](gateway-auth.yaml) **critical** | JWT validation \\| tenant context | beta (stable until aaaaaaa) | stable | 2027-01-20 ready for stable | no acceptance criteria; plan not approved; structure not approved; not checked: T-12 | 2027-02-01 at aaaaaaa, 1 files changed since | HAMSP-23 |",
		"| [`tenant-suspend`](tenant-suspend.yaml) | Suspend a tenant | — | alpha | — | no planned test: AC-2; plan not approved; not verified | — |  |",
		endMarker + "\n\nMore text.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("map lacks\n%s\n\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old") {
		t.Error("the old block was kept")
	}
}

func TestRunRejectsBrokenRecord(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, featuresDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, readme), []byte(readmeDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFeature(t, root, "a", `feature: a
title: A
target: alpha
stage: alpha
code: ["apps/a/**"]
checklist:
  T-01:
    verified: [apps/a/a_test.go:TestCross]
`)
	repo := fakeRepo{files: map[string]string{"apps/b/b.go": ""}}
	err := run(root, repo, false)
	if err == nil {
		t.Fatal("want errors")
	}
	for _, want := range []string{`code: "apps/a/**" matches no file`, `T-01: test "apps/a/a_test.go:TestCross": file not found`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("errors lack %q:\n%v", want, err)
		}
	}
	doc, _ := os.ReadFile(filepath.Join(root, readme))
	if string(doc) != readmeDoc {
		t.Error("the map was written despite errors")
	}
}
