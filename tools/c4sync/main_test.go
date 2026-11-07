package main

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/archlint"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/check"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/config"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/model"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/pipeline"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite testdata/fixture/golden from the code")

func c4sync(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	args = append(args[:1:1], append([]string{"-go-arch-lint", testutil.GoArchLint(t)}, args[1:]...)...)
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func build(t *testing.T, cfgPath string) *pipeline.Result {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	res, err := pipeline.Build(cfg, archlint.Runner{Bin: testutil.GoArchLint(t)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// AC (HAMSP-74, HAMSP-75): the fixture gives the golden files, and the same
// input always gives byte-identical output.
func TestGenerateMatchesGolden(t *testing.T) {
	cfgPath := filepath.Join(testutil.Fixture(t), "c4sync.yml")
	first := build(t, cfgPath)
	second := build(t, cfgPath)
	if len(first.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(first.Files))
	}
	for path, content := range first.Files {
		if second.Files[path] != content {
			t.Errorf("%s: two runs differ", path)
		}
		if *update {
			testutil.Write(t, path, content)
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != content {
			t.Errorf("%s differs from the code; run go test -run TestGenerateMatchesGolden -update", path)
		}
	}
}

// AC (HAMSP-74): the mapping comes from go-arch-lint; a package outside every
// component is listed as unmapped.
func TestMappingAndUnmapped(t *testing.T) {
	dir := testutil.CopyFixture(t)
	res := build(t, filepath.Join(dir, "c4sync.yml"))
	if got := res.Mapping.Components["app"]; strings.Join(got, " ") != "example.com/fixture/internal/app example.com/fixture/internal/app/events" {
		t.Fatalf("app packages: %v", got)
	}
	if len(res.Mapping.Unmapped) != 0 {
		t.Fatalf("fixture excludes internal/tools, got unmapped %v", res.Mapping.Unmapped)
	}

	testutil.Replace(t, filepath.Join(dir, ".go-arch-lint.yml"), "exclude:\n  - internal/tools\n", "")
	code, out, _ := c4sync(t, "check", "-config", filepath.Join(dir, "c4sync.yml"))
	if code != check.Violations || !strings.Contains(out, "unmapped package: example.com/fixture/internal/tools") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	code, out, _ = c4sync(t, "generate", "-config", filepath.Join(dir, "c4sync.yml"))
	if code != 0 || !strings.Contains(out, "unmapped package: example.com/fixture/internal/tools") {
		t.Fatalf("generate: exit %d, output:\n%s", code, out)
	}
}

// AC (HAMSP-75, HAMSP-76): what the extractor and the detectors find in the fixture.
func TestModelFacts(t *testing.T) {
	m := build(t, filepath.Join(testutil.Fixture(t), "c4sync.yml")).Model

	rel := func(from, to string) *model.Relation {
		for i := range m.Relations {
			if m.Relations[i].From == from && m.Relations[i].To == to {
				return &m.Relations[i]
			}
		}
		return nil
	}
	// A relation lists the importing file and line.
	r := rel("api", "app")
	if r == nil || len(r.Evidence) != 1 || r.Evidence[0].Pos != (model.Pos{File: "internal/api/api.go", Line: 7}) {
		t.Errorf("api -> app evidence: %+v", r)
	}
	// An import cycle between components is two relations, not an error.
	if rel("app", "bus") == nil || rel("bus", "app") == nil {
		t.Errorf("want app <-> bus, got %+v", m.Relations)
	}
	// A library mapped in c4sync.yml gives a relation to its element.
	var ext []string
	for _, e := range m.ExternalRelations {
		ext = append(ext, e.From+"->"+e.To)
	}
	if strings.Join(ext, " ") != "store->sys.db" {
		t.Errorf("external relations: %v", ext)
	}
	// An interface implemented in another package, which does not import it.
	found := false
	for _, cr := range m.CodeRelations {
		if cr.From == "internal/store.SQLStore" && cr.To == "internal/app.EventStore" && cr.Kind == model.Implements {
			found = true
		}
	}
	if !found {
		t.Errorf("want store.SQLStore implements app.EventStore, got %+v", m.CodeRelations)
	}

	ifaces := map[string]bool{}
	for _, i := range m.Interfaces {
		name := i.Name
		if i.Unresolved {
			name = "?" + i.Where
		}
		key := i.Component + " " + i.Kind + " " + i.Direction + " " + name
		if ifaces[key] {
			t.Errorf("duplicate interface %s", key)
		}
		ifaces[key] = true
	}
	for _, want := range []string{
		"api http serves POST /v1/events", // every route goes to the component that registers it
		"api http serves GET /healthz",
		"api http serves ?Routes",          // built at runtime
		"app topic produces events.v1",     // constant from another package
		"bus topic produces ?Announce",     // fmt.Sprintf is unresolved
		"bus topic produces dead-letter",   // struct field matcher
		"bus topic consumes audit.v1",      // variadic argument
		"bus topic consumes events.v1",     //
		"store table writes events",        // query
		"store table reads tenants",        //
		"migrations table defines events",  // migration file
		"migrations table defines tenants", //
	} {
		if !ifaces[want] {
			t.Errorf("missing interface %q", want)
		}
	}
}

// AC (HAMSP-77): on an unchanged fixture, check passes and prints nothing.
func TestCheckPassesOnFixture(t *testing.T) {
	code, out, errOut := c4sync(t, "check", "-config", filepath.Join(testutil.Fixture(t), "c4sync.yml"))
	if code != check.OK || out != "" {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

// AC (HAMSP-77): a new import between two components, without regenerating,
// fails as stale and shows the new relation; the import breaks a go-arch-lint
// rule too, which is reported with file and line.
func TestCheckStaleAndRuleViolation(t *testing.T) {
	dir := testutil.CopyFixture(t)
	testutil.Replace(t, filepath.Join(dir, "internal/api/api.go"),
		`"example.com/fixture/internal/app"`,
		"\"example.com/fixture/internal/app\"\n\t_ \"example.com/fixture/internal/store\"")
	code, out, _ := c4sync(t, "check", "-config", filepath.Join(dir, "c4sync.yml"))
	if code != check.Stale|check.Violations {
		t.Fatalf("want exit %d, got %d\n%s", check.Stale|check.Violations, code, out)
	}
	for _, want := range []string{
		"stale: ",
		"+fx_api -> fx_store \"Uses\" \"Go import\" \"c4sync\"",
		"internal/api/api.go:8: component api must not import example.com/fixture/internal/store",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	code, out, _ = c4sync(t, "check", "-json", "-config", filepath.Join(dir, "c4sync.yml"))
	if code != check.Stale|check.Violations || !strings.Contains(out, `"stale"`) || !strings.Contains(out, `"violations"`) {
		t.Fatalf("json: exit %d\n%s", code, out)
	}
}

// AC (HAMSP-77): only stale output gives exit code 2.
func TestCheckOnlyStale(t *testing.T) {
	dir := testutil.CopyFixture(t)
	testutil.Replace(t, filepath.Join(dir, "golden/components.dsl"), "fx_api -> fx_app", "fx_api -> fx_bus")
	code, out, _ := c4sync(t, "check", "-config", filepath.Join(dir, "c4sync.yml"))
	if code != check.Stale || !strings.Contains(out, "-fx_api -> fx_bus") || !strings.Contains(out, "+fx_api -> fx_app") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// AC (HAMSP-74, HAMSP-77): c4sync.yml metadata for a component that
// go-arch-lint does not define fails with the file and line.
func TestUnknownComponentInConfig(t *testing.T) {
	dir := testutil.CopyFixture(t)
	cfg := filepath.Join(dir, "c4sync.yml")
	testutil.Replace(t, cfg, "  store:\n    name: Store\n", "  store:\n    name: Store\n  ghost:\n    name: Ghost\n")
	code, _, errOut := c4sync(t, "check", "-config", cfg)
	if code != check.Error || !strings.Contains(errOut, cfg+":13:") || !strings.Contains(errOut, `"ghost"`) {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
}

// AC (HAMSP-78): diff between two source trees reports the change, important
// components first, and writes a views workspace; identical trees report nothing.
func TestDiff(t *testing.T) {
	base := filepath.Join(testutil.Fixture(t), "c4sync.yml")
	code, out, errOut := c4sync(t, "diff", "-config", base, "-base-config", base)
	if code != 0 || out != "" {
		t.Fatalf("identical trees: exit %d\n%s%s", code, out, errOut)
	}

	dir := testutil.CopyFixture(t)
	testutil.Replace(t, filepath.Join(dir, "internal/store/store.go"),
		`const insertEvent = "INSERT INTO events (id) VALUES ($1)"`,
		`const insertEvent = "INSERT INTO events_v2 (id) VALUES ($1)"`)
	testutil.Replace(t, filepath.Join(dir, "internal/api/api.go"),
		`"example.com/fixture/internal/app"`,
		"\"example.com/fixture/internal/app\"\n\t\"example.com/fixture/internal/bus\"")
	testutil.Replace(t, filepath.Join(dir, "internal/api/api.go"),
		"func Routes(", "var _ bus.Client\n\n// Routes registers the handlers.\nfunc Routes(")
	views := filepath.Join(t.TempDir(), "changes.dsl")
	jsonOut := filepath.Join(t.TempDir(), "changes.json")
	code, out, errOut = c4sync(t, "diff", "-config", filepath.Join(dir, "c4sync.yml"), "-base-config", base,
		"-views", views, "-json", jsonOut)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	storeAt := strings.Index(out, "### store (important)")
	apiAt := strings.Index(out, "### api")
	if storeAt < 0 || apiAt < 0 || storeAt > apiAt {
		t.Fatalf("store must come first:\n%s", out)
	}
	for _, want := range []string{
		"- added interface: store writes table events_v2 (`internal/store/store.go:18`)",
		"- removed interface: store writes table events",
		"- added relation: api → bus (`internal/api/api.go:8`)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	for _, f := range []string{views, jsonOut} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if os.Getenv("STRUCTURIZR_WAR") != "" {
		structurizrValidate(t, views)
	}
}

// AC (HAMSP-74, HAMSP-78): Structurizr accepts the C3 fragment inside the
// fixture workspace, the code workspace and a diff views workspace, and
// exports them to Mermaid. Needs Java 21+ and STRUCTURIZR_WAR.
func TestStructurizrValidates(t *testing.T) {
	if os.Getenv("STRUCTURIZR_WAR") == "" {
		t.Skip("STRUCTURIZR_WAR is not set")
	}
	fixture := testutil.Fixture(t)
	for _, ws := range []string{"workspace.dsl", "golden/code.dsl"} {
		structurizrValidate(t, filepath.Join(fixture, ws))
		out := t.TempDir()
		java(t, "export", "-workspace", filepath.Join(fixture, ws), "-format", "mermaid", "-output", out)
		files, _ := filepath.Glob(filepath.Join(out, "*.mmd"))
		if len(files) == 0 {
			t.Errorf("%s: no Mermaid views exported", ws)
		}
	}
}

func structurizrValidate(t *testing.T, ws string) {
	t.Helper()
	java(t, "validate", "-workspace", ws)
}

func java(t *testing.T, args ...string) {
	t.Helper()
	bin := os.Getenv("JAVA")
	if bin == "" {
		bin = "java"
	}
	cmd := exec.Command(bin, append([]string{"-jar", os.Getenv("STRUCTURIZR_WAR")}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil || bytes.Contains(out, []byte("ERROR")) {
		t.Fatalf("structurizr %v: %v\n%s", args, err, out)
	}
}

// A component declared in .go-arch-lint.yml whose package does not exist yet
// is not an error, and gets no box.
func TestDeclaredComponentWithoutPackages(t *testing.T) {
	dir := testutil.CopyFixture(t)
	testutil.Replace(t, filepath.Join(dir, ".go-arch-lint.yml"),
		"  migrations: { in: internal/migrations }\n",
		"  migrations: { in: internal/migrations }\n  billing:    { in: internal/billing }\n")
	testutil.Replace(t, filepath.Join(dir, "c4sync.yml"), "  store:\n    name: Store\n", "  store:\n    name: Store\n  billing:\n    name: Billing\n")
	res := build(t, filepath.Join(dir, "c4sync.yml"))
	if res.Model.Component("billing") != nil {
		t.Fatalf("billing has no packages and must not be drawn")
	}
}
