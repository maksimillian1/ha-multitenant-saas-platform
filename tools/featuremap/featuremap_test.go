package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fakeRepo is a repository in memory.
type fakeRepo struct {
	files   map[string]string
	changed map[string][]string // files changed since a commit
	pr      []string            // files changed by the pull request
}

func (r fakeRepo) Files() ([]string, error) {
	var out []string
	for p := range r.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func (r fakeRepo) ChangedSince(commit string) ([]string, error) { return r.changed[commit], nil }
func (r fakeRepo) ChangedInPR(string) ([]string, error)         { return r.pr, nil }

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

// Verifies: feature-map AC-3
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

// alphaGates meets every gate alpha needs.
const alphaGates = `gates:
  G-01: {status: pass, by: verify, date: 2026-11-07, commit: abcdef12}
  G-02: {status: pass, by: verify, date: 2026-11-07, commit: abcdef12}
  G-03: {status: n/a, by: verify, date: 2026-11-07, note: no schema}
  G-04: {status: pass, by: verify, date: 2026-11-07, level: basic}
  G-07: {status: pass, by: owner, date: 2026-11-01}
`

const g08 = "  G-08: {status: pass, by: owner, date: 2026-11-02}\n"

// Verifies: feature-map AC-2
func TestValidate(t *testing.T) {
	full := strings.Replace(alphaGates, "level: basic", "level: full", 1)
	cases := []struct {
		name, slug, body, wantErr string
	}{
		{"name differs from the file", "tenant-suspend", "feature: other\ntitle: X\n", "must equal the file name"},
		{"no title", "a", "feature: a\n", "title is missing"},
		{"unknown field", "a", "feature: a\ntitle: X\ntarget: beta\n", "field target not found"},
		{"bad stage", "a", "feature: a\ntitle: X\nstage: gamma\n", `stage "gamma" must be proto, alpha, beta or stable`},
		{"stage without gates", "a", "feature: a\ntitle: X\nstage: alpha\ncode: [x/**]\n", "stage alpha is not backed by its gates: G-01 not recorded; G-02 not recorded"},
		{"failed gate", "a", "feature: a\ntitle: X\nstage: alpha\ncode: [x/**]\n" + strings.Replace(alphaGates, "G-02: {status: pass", "G-02: {status: fail", 1), "G-02 is fail"},
		{"basic checklist for beta", "a", "feature: a\ntitle: X\nstage: beta\ncode: [x/**]\n" + alphaGates + g08, "G-04 is basic, needs full"},
		{"stable without a review", "a", "feature: a\ntitle: X\nstage: stable\ncode: [x/**]\n" + full + g08, "G-06 not recorded"},
		{"review without a commit", "a", "feature: a\ntitle: X\ngates:\n  G-06: {status: pass, by: owner, date: 2027-01-01}\n", "a review needs the commit"},
		{"n/a without a reason", "a", "feature: a\ntitle: X\ngates:\n  G-03: {status: n/a, by: verify, date: 2026-11-07}\n", "n/a needs a note"},
		{"unknown gate", "a", "feature: a\ntitle: X\ngates:\n  G-09: {status: pass, by: owner, date: 2026-11-07}\n", "not G-01 to G-08"},
		{"bad gate status", "a", "feature: a\ntitle: X\ngates:\n  G-01: {status: ok, by: owner, date: 2026-11-07}\n", "must be pass, fail, n/a or not-checked"},
		{"bad gate author", "a", "feature: a\ntitle: X\ngates:\n  G-01: {status: pass, by: agent, date: 2026-11-07}\n", "must be verify or owner"},
		{"stage without code", "a", "feature: a\ntitle: X\nstage: alpha\n" + alphaGates, "needs code globs"},
		{"bad checklist item", "a", "feature: a\ntitle: X\nchecklist:\n  T-13:\n    planned: applies\n", "T-01 to T-12"},
		{"acceptance n/a", "a", "feature: a\ntitle: X\nacceptance:\n  AC-1:\n    text: x\n    verified: n/a — no\n", "cannot be n/a"},
		{"bad verified", "a", "feature: a\ntitle: X\nchecklist:\n  T-01:\n    verified: done\n", "list of tests"},
		{"alpha by verify and owner", "a", "feature: a\ntitle: X\nstage: alpha\ncode: [x/**]\n" + alphaGates, ""},
		{"beta straight away", "a", "feature: a\ntitle: X\nstage: beta\ncode: [x/**]\n" + full + g08, ""},
		{"a gate marked by the owner alone", "a", "feature: a\ntitle: X\nstage: proto\ncode: [x/**]\ngates:\n  G-01: {status: pass, by: owner, date: 2026-11-07}\n", ""},
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

func setupRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, featuresDir), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

const gatewayAuth = `feature: gateway-auth
title: JWT validation and tenant context
stage: stable
critical: true
code: ["apps/gateway/auth/**"]
links: {issues: [HAMSP-23]}
gates:
  G-02: {status: pass, by: verify, date: 2027-01-20, commit: bbbbbbb2}
  G-03: {status: n/a, by: verify, date: 2027-01-20, note: no schema}
  G-04: {status: pass, by: verify, date: 2027-01-20, level: full}
  G-05: {status: not-checked, by: verify, date: 2027-01-20}
  G-06: {status: pass, by: owner, date: 2027-02-01, commit: aaaaaaa1}
  G-07: {status: pass, by: owner, date: 2027-01-02}
  G-08: {status: pass, by: owner, date: 2027-01-03}
runs:
  - {date: 2027-01-20, commit: bbbbbbb2, stage_checked: stable, verdict: ready}
`

const tenantSuspend = `feature: tenant-suspend
title: Suspend a tenant
acceptance:
  AC-1:
    text: A suspended tenant gets 403.
    level: integration
    planned: apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403
    verified: [apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403]
gates:
  G-07: {status: fail, by: verify, date: 2026-11-20}
runs:
  - {date: 2026-11-20, commit: ccccccc3, stage_checked: alpha, verdict: not-ready, failed: [G-07]}
`

// Verifies: feature-map AC-1
func TestRun(t *testing.T) {
	root := setupRoot(t)
	writeFeature(t, root, "gateway-auth", gatewayAuth)
	writeFeature(t, root, "tenant-suspend", tenantSuspend)
	repo := fakeRepo{
		files: map[string]string{
			"apps/gateway/auth/auth.go":            "package auth",
			"apps/gateway/suspend/suspend_test.go": testFile,
		},
		// The map must not depend on git: a change to reviewed code does
		// not change the map.
		changed: map[string][]string{"aaaaaaa1": {"apps/gateway/auth/auth.go"}},
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

	doc, _ := os.ReadFile(filepath.Join(root, mapFile))
	got := string(doc)
	for _, want := range []string{
		"<!-- Generated by tools/featuremap",
		"| Feature | Stage | G-01 | G-02 | G-03 | G-04 | G-05 | G-06 | G-07 | G-08 | Last verify | Issues |",
		"| [`gateway-auth`](gateway-auth.yaml) **critical** | stable |  | ✓ | n/a | ✓ full | ? | ✓ (owner) | ✓ (owner) | ✓ (owner) | 2027-01-20 ready for stable | HAMSP-23 |",
		"| [`tenant-suspend`](tenant-suspend.yaml) | — |  |  |  |  |  |  | ✗ |  | 2026-11-20 not-ready for alpha |  |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("map lacks\n%s\n\ngot:\n%s", want, got)
		}
	}

	// A feature file changes: the committed map is now out of date.
	writeFeature(t, root, "tenant-suspend", strings.Replace(tenantSuspend, "status: fail", "status: pass", 1))
	if err := run(root, repo, true); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("check after a feature file changed: got %v, want out of date", err)
	}
}

// Verifies: feature-map AC-3
func TestRunRejectsBrokenRecord(t *testing.T) {
	root := setupRoot(t)
	writeFeature(t, root, "a", "feature: a\ntitle: A\nstage: alpha\ncode: [\"apps/a/**\"]\nchecklist:\n  T-01:\n    verified: [apps/a/a_test.go:TestCross]\n"+alphaGates)
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
	if _, err := os.Stat(filepath.Join(root, mapFile)); !os.IsNotExist(err) {
		t.Error("the map was written despite errors")
	}
}

// Verifies: feature-map AC-4
func TestPRReport(t *testing.T) {
	root := setupRoot(t)
	writeFeature(t, root, "gateway-auth", gatewayAuth)
	writeFeature(t, root, "tenant-suspend", tenantSuspend+"code: [\"apps/gateway/suspend/**\"]\n")
	writeFeature(t, root, "untouched", "feature: untouched\ntitle: U\ncode: [\"apps/other/**\"]\n")
	repo := fakeRepo{
		pr:      []string{"apps/gateway/auth/auth.go", "apps/gateway/auth/jwks.go", "apps/gateway/suspend/suspend.go", "README.md"},
		changed: map[string][]string{"aaaaaaa1": {"apps/gateway/auth/auth.go", "apps/gateway/auth/jwks.go", "apps/gateway/auth/old.go"}},
	}
	before, _ := os.ReadFile(filepath.Join(root, featuresDir, "gateway-auth.yaml"))

	var out strings.Builder
	if err := report(root, repo, "origin/master", &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		prMarker + "\n### Features touched by this pull request",
		"| `gateway-auth` | stable | yes | 2 | 2027-02-01 at aaaaaaa | 3 |",
		"| `tenant-suspend` | — |  | 1 | — | — |",
		"No stage is changed automatically.",
		"* `apps/gateway/auth/jwks.go`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks\n%s\n\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(got, "untouched") {
		t.Error("a feature the PR does not touch is listed")
	}
	after, _ := os.ReadFile(filepath.Join(root, featuresDir, "gateway-auth.yaml"))
	if string(before) != string(after) {
		t.Error("the report changed a feature file")
	}

	var none strings.Builder
	if err := report(root, fakeRepo{pr: []string{"README.md"}}, "origin/master", &none); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(none.String(), "No code of a feature") {
		t.Errorf("report for an unrelated PR:\n%s", none.String())
	}
}

// Verifies: feature-map AC-4
func TestGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, body string) {
		t.Helper()
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main")
	write("auth/auth.go", "package auth\n")
	write("other/other.go", "package other\n")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	reviewed := git("rev-parse", "HEAD")

	git("checkout", "-q", "-b", "pr")
	write("auth/auth.go", "package auth\n// changed\n")
	git("commit", "-q", "-am", "change auth")
	write("auth/new.go", "package auth\n") // untracked
	write("other/other.go", "package other\n// uncommitted\n")

	repo := gitRepo{root: dir}
	since, err := repo.ChangedSince(reviewed)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(since)
	if strings.Join(since, ",") != "auth/auth.go,auth/new.go,other/other.go" {
		t.Errorf("ChangedSince: got %v", since)
	}
	pr, err := repo.ChangedInPR("main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pr, ",") != "auth/auth.go" {
		t.Errorf("ChangedInPR: got %v, want only the committed change", pr)
	}
	files, err := repo.Files()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if strings.Join(files, ",") != "auth/auth.go,auth/new.go,other/other.go" {
		t.Errorf("Files: got %v", files)
	}
}
