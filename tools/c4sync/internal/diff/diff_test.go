package diff

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/model"
)

func base() *model.Model {
	return &model.Model{
		Container: "Svc",
		Module:    "example.com/m",
		Components: []model.Component{
			{ID: "api", Packages: []string{"example.com/m/api"}},
			{ID: "app", Packages: []string{"example.com/m/app"}},
			{ID: "store", Packages: []string{"example.com/m/store"}},
		},
		Relations: []model.Relation{{From: "api", To: "app", Kind: model.Imports}},
	}
}

// AC (HAMSP-78): identical models give an empty report.
// Verifies: c4-from-code AC-9
func TestIdenticalModelsGiveEmptyReport(t *testing.T) {
	r := Compare(base(), base(), nil)
	if !r.Empty() {
		t.Fatalf("want empty, got %+v", r.Changes)
	}
	var b bytes.Buffer
	r.WriteMarkdown(&b)
	if b.Len() != 0 {
		t.Fatalf("empty report printed %q", b.String())
	}
}

// AC (HAMSP-78): models that differ by one relation give a report with exactly that relation.
// Verifies: c4-from-code AC-9
func TestOneRelation(t *testing.T) {
	head := base()
	head.Relations = append(head.Relations, model.Relation{From: "app", To: "store", Kind: model.Imports,
		Evidence: []model.Evidence{{Pos: model.Pos{File: "app/app.go", Line: 7}}}})
	r := Compare(base(), head, nil)
	if len(r.Changes) != 1 {
		t.Fatalf("want 1 change, got %+v", r.Changes)
	}
	c := r.Changes[0]
	if c.Kind != KindRelation || c.Op != Added || c.Text != "app → store" || c.Where != "app/app.go:7" {
		t.Fatalf("got %+v", c)
	}
	if got := r.Components(head); len(got) != 2 || got[0] != "app" || got[1] != "store" {
		t.Fatalf("components %v", got)
	}
}

// AC (HAMSP-78): a change to an important component is listed before other changes.
// Verifies: c4-from-code AC-9
func TestImportantFirst(t *testing.T) {
	head := base()
	head.Relations = append(head.Relations,
		model.Relation{From: "api", To: "store", Kind: model.Imports},
		model.Relation{From: "app", To: "api", Kind: model.Imports})
	head.Interfaces = []model.ExternalInterface{{Component: "store", Kind: model.Table, Direction: model.Writes, Name: "events"}}
	r := Compare(base(), head, []string{"store"})
	if len(r.Changes) != 3 {
		t.Fatalf("got %+v", r.Changes)
	}
	if !r.Changes[0].Important || !r.Changes[1].Important || r.Changes[2].Important {
		t.Fatalf("important changes must come first: %+v", r.Changes)
	}
	if r.Changes[2].Text != "app → api" {
		t.Fatalf("got %+v", r.Changes[2])
	}
	var b bytes.Buffer
	r.WriteMarkdown(&b)
	if !strings.Contains(b.String(), "(important)") || strings.Index(b.String(), "store") > strings.Index(b.String(), "app → api") {
		t.Fatalf("markdown:\n%s", b.String())
	}
}

// Verifies: c4-from-code AC-9
func TestMovedPackageAndChangedElement(t *testing.T) {
	b := base()
	b.CodeElements = []model.CodeElement{{ID: "app.S", Component: "app", Package: "example.com/m/app", Name: "S", Kind: model.Struct, Methods: []string{"A()"}}}
	head := base()
	head.Components[1].Packages = nil
	head.Components[2].Packages = append(head.Components[2].Packages, "example.com/m/app")
	head.CodeElements = []model.CodeElement{{ID: "app.S", Component: "app", Package: "example.com/m/app", Name: "S", Kind: model.Struct, Methods: []string{"A()", "B() error"}}}
	r := Compare(b, head, nil)
	var texts []string
	for _, c := range r.Changes {
		texts = append(texts, c.Op+" "+c.Kind+": "+c.Text)
	}
	got := strings.Join(texts, "\n")
	for _, want := range []string{
		"moved package: package app moved from app to store",
		"changed code element: struct app.S: +B() error",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

// A new component lists its packages relative to the module.
// Verifies: c4-from-code AC-9
func TestAddedComponentUsesRelativePackages(t *testing.T) {
	head := &model.Model{Module: "example.com/m", Components: []model.Component{
		{ID: "api", Packages: []string{"example.com/m", "example.com/m/internal/api"}},
	}}
	r := Compare(&model.Model{}, head, nil)
	if len(r.Changes) != 1 || r.Changes[0].Text != "api (., internal/api)" {
		t.Fatalf("got %+v", r.Changes)
	}
}

// All changes of a component sit under one heading, even when only some of
// them touch an important component.
// Verifies: c4-from-code AC-9
func TestOneHeadingPerComponent(t *testing.T) {
	base := &model.Model{Components: []model.Component{{ID: "store"}, {ID: "event"}}}
	head := &model.Model{
		Components: []model.Component{{ID: "store"}, {ID: "event"}, {ID: "writer"}},
		Relations: []model.Relation{
			{From: "writer", To: "store", Kind: model.Imports},
			{From: "writer", To: "event", Kind: model.Imports},
		},
	}
	var b strings.Builder
	Compare(base, head, []string{"store"}).WriteMarkdown(&b)
	if n := strings.Count(b.String(), "### writer"); n != 1 {
		t.Fatalf("want one writer heading, got %d:\n%s", n, b.String())
	}
	if !strings.Contains(b.String(), "### writer (important)") {
		t.Fatalf("writer touches store and must be marked:\n%s", b.String())
	}
}
