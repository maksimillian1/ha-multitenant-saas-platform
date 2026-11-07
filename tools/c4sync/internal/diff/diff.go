// Package diff compares two models and reports structural changes, so a
// reviewer reads what changed in the structure instead of the code.
package diff

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/model"
)

// Change kinds.
const (
	KindComponent    = "component"
	KindPackage      = "package"
	KindRelation     = "relation"
	KindExternal     = "external relation"
	KindInterface    = "interface"
	KindElement      = "code element"
	KindCodeRelation = "code relation"
	KindUnmapped     = "unmapped package"
)

// Change operations.
const (
	Added   = "added"
	Removed = "removed"
	Changed = "changed"
	Moved   = "moved"
)

// Change is one structural difference.
type Change struct {
	Kind       string   `json:"kind"`
	Op         string   `json:"op"`
	Component  string   `json:"component"`            // the component the change belongs to
	Components []string `json:"components,omitempty"` // every component it touches
	Text       string   `json:"text"`
	Where      string   `json:"where,omitempty"` // file:line in the tree the item comes from
	Important  bool     `json:"important"`
}

// Report is the list of changes, important ones first.
type Report struct {
	Name    string   `json:"name"`
	Changes []Change `json:"changes"`
}

// Empty says whether there is no structural change.
func (r *Report) Empty() bool { return len(r.Changes) == 0 }

// Components returns the components touched by the changes that still exist
// in the new model, in report order.
func (r *Report) Components(head *model.Model) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range r.Changes {
		for _, id := range c.Components {
			if !seen[id] && head.Component(id) != nil {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// Compare lists the changes from base to head. important components are listed first.
func Compare(base, head *model.Model, important []string) *Report {
	imp := map[string]bool{}
	for _, c := range important {
		imp[c] = true
	}
	r := &Report{Name: head.Container}
	add := func(c Change) {
		if len(c.Components) == 0 {
			c.Components = []string{c.Component}
		}
		for _, id := range c.Components {
			if imp[id] {
				c.Important = true
			}
		}
		r.Changes = append(r.Changes, c)
	}

	// Components and their packages.
	bc, hc := componentsByID(base), componentsByID(head)
	for _, id := range keys(hc) {
		if _, ok := bc[id]; !ok {
			add(Change{Kind: KindComponent, Op: Added, Component: id, Text: fmt.Sprintf("%s (%s)", id, strings.Join(relPkgs(head.Module, hc[id].Packages), ", "))})
		}
	}
	for _, id := range keys(bc) {
		if _, ok := hc[id]; !ok {
			add(Change{Kind: KindComponent, Op: Removed, Component: id, Text: "component " + id})
		}
	}
	bp, hp := packageOwners(base), packageOwners(head)
	for _, p := range keys(hp) {
		switch old, ok := bp[p]; {
		case !ok:
			if _, existed := bc[hp[p]]; existed {
				add(Change{Kind: KindPackage, Op: Added, Component: hp[p], Text: fmt.Sprintf("package %s in %s", relPkg(head.Module, p), hp[p])})
			}
		case old != hp[p]:
			add(Change{Kind: KindPackage, Op: Moved, Component: hp[p], Components: []string{hp[p], old},
				Text: fmt.Sprintf("package %s moved from %s to %s", relPkg(head.Module, p), old, hp[p])})
		}
	}
	for _, p := range keys(bp) {
		if _, ok := hp[p]; !ok {
			if _, still := hc[bp[p]]; still {
				add(Change{Kind: KindPackage, Op: Removed, Component: bp[p], Text: fmt.Sprintf("package %s in %s", relPkg(base.Module, p), bp[p])})
			}
		}
	}

	// Relations between components.
	type rel struct{ from, to string }
	brs, hrs := map[rel]model.Relation{}, map[rel]model.Relation{}
	for _, x := range base.Relations {
		brs[rel{x.From, x.To}] = x
	}
	for _, x := range head.Relations {
		hrs[rel{x.From, x.To}] = x
	}
	for _, x := range head.Relations {
		if _, ok := brs[rel{x.From, x.To}]; !ok {
			add(Change{Kind: KindRelation, Op: Added, Component: x.From, Components: []string{x.From, x.To},
				Text: fmt.Sprintf("%s → %s", x.From, x.To), Where: firstEvidence(x.Evidence)})
		}
	}
	for _, x := range base.Relations {
		if _, ok := hrs[rel{x.From, x.To}]; !ok {
			add(Change{Kind: KindRelation, Op: Removed, Component: x.From, Components: []string{x.From, x.To},
				Text: fmt.Sprintf("%s → %s", x.From, x.To)})
		}
	}

	// Relations to external elements.
	ext := func(x model.ExternalRelation) string {
		return fmt.Sprintf("%s → %s: %s (%s)", x.From, x.To, x.Description, x.Technology)
	}
	diffSets(base.ExternalRelations, head.ExternalRelations, ext, func(x model.ExternalRelation, op string) {
		add(Change{Kind: KindExternal, Op: op, Component: x.From, Text: ext(x), Where: whereIf(op, firstEvidence(x.Evidence))})
	})

	// External interfaces.
	iface := func(x model.ExternalInterface) string {
		name := x.Name
		if x.Unresolved {
			name = "? (in " + x.Where + ")"
		}
		return fmt.Sprintf("%s %s %s %s", x.Component, x.Direction, x.Kind, name)
	}
	diffSets(base.Interfaces, head.Interfaces, iface, func(x model.ExternalInterface, op string) {
		add(Change{Kind: KindInterface, Op: op, Component: x.Component, Text: iface(x), Where: whereIf(op, x.Pos.String())})
	})

	// Code elements.
	be, he := elementsByID(base), elementsByID(head)
	for _, id := range keys(he) {
		e := he[id]
		old, ok := be[id]
		switch {
		case !ok:
			add(Change{Kind: KindElement, Op: Added, Component: e.Component, Text: fmt.Sprintf("%s %s.%s", e.Kind, shortPkg(e.Package), e.Name), Where: e.Pos.String()})
		case old.Kind != e.Kind || strings.Join(old.Methods, "\n") != strings.Join(e.Methods, "\n"):
			add(Change{Kind: KindElement, Op: Changed, Component: e.Component, Text: fmt.Sprintf("%s %s.%s: %s", e.Kind, shortPkg(e.Package), e.Name, methodDiff(old.Methods, e.Methods)), Where: e.Pos.String()})
		case old.Component != e.Component:
			add(Change{Kind: KindElement, Op: Moved, Component: e.Component, Components: []string{e.Component, old.Component},
				Text: fmt.Sprintf("%s %s.%s moved from %s", e.Kind, shortPkg(e.Package), e.Name, old.Component)})
		}
	}
	for _, id := range keys(be) {
		if _, ok := he[id]; !ok {
			e := be[id]
			add(Change{Kind: KindElement, Op: Removed, Component: e.Component, Text: fmt.Sprintf("%s %s.%s", e.Kind, shortPkg(e.Package), e.Name)})
		}
	}

	// Code relations.
	cr := func(x model.CodeRelation) string { return fmt.Sprintf("%s %s %s", x.From, x.Kind, x.To) }
	diffSets(base.CodeRelations, head.CodeRelations, cr, func(x model.CodeRelation, op string) {
		elems := he
		if op == Removed {
			elems = be
		}
		from, to := elems[x.From].Component, elems[x.To].Component
		comps := []string{from}
		if to != from {
			comps = append(comps, to)
		}
		add(Change{Kind: KindCodeRelation, Op: op, Component: from, Components: comps, Text: cr(x), Where: whereIf(op, firstEvidence(x.Evidence))})
	})

	// Packages outside every component.
	diffSets(base.Unmapped, head.Unmapped, func(s string) string { return s }, func(p, op string) {
		add(Change{Kind: KindUnmapped, Op: op, Text: p})
	})

	// A component is listed first, under one heading, when any of its
	// changes touches an important component.
	impComp := map[string]bool{}
	for _, c := range r.Changes {
		if c.Important {
			impComp[c.Component] = true
		}
	}
	sort.SliceStable(r.Changes, func(i, j int) bool {
		a, b := r.Changes[i], r.Changes[j]
		if impComp[a.Component] != impComp[b.Component] {
			return impComp[a.Component]
		}
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		if a.Op != b.Op {
			return a.Op < b.Op
		}
		return a.Text < b.Text
	})
	return r
}

var rank = map[string]int{
	KindComponent: 0, KindPackage: 1, KindRelation: 2, KindExternal: 3,
	KindInterface: 4, KindElement: 5, KindCodeRelation: 6, KindUnmapped: 7,
}

// WriteMarkdown prints the report grouped by component, important first.
// Each component is a collapsible <details> block, so a long report stays
// short on a pull request page: important components are open, the others
// show one line with the number of changes. An empty report prints nothing.
func (r *Report) WriteMarkdown(w io.Writer) {
	if r.Empty() {
		return
	}
	type section struct {
		name      string
		important bool
		changes   []Change
	}
	var sections []*section
	byName := map[string]*section{}
	for _, c := range r.Changes {
		s := byName[c.Component]
		if s == nil {
			s = &section{name: c.Component}
			if s.name == "" {
				s.name = "(no component)"
			}
			byName[c.Component] = s
			sections = append(sections, s)
		}
		s.important = s.important || c.Important
		s.changes = append(s.changes, c)
	}
	nImportant := 0
	for _, s := range sections {
		if s.important {
			nImportant++
		}
	}

	fmt.Fprintf(w, "## Structure changes: %s\n\n", r.Name)
	fmt.Fprintf(w, "%s changed", plural(len(sections), "component"))
	if nImportant > 0 {
		fmt.Fprintf(w, ", %d important", nImportant)
	}
	fmt.Fprintf(w, "; %s.\n", plural(len(r.Changes), "change"))
	for _, s := range sections {
		open, mark := "", ""
		if s.important {
			open, mark = " open", " (important)"
		}
		fmt.Fprintf(w, "\n<details%s>\n<summary><b>%s</b>%s: %s</summary>\n\n", open, s.name, mark, plural(len(s.changes), "change"))
		for _, c := range s.changes {
			line := fmt.Sprintf("- %s %s: %s", c.Op, c.Kind, c.Text)
			if c.Where != "" {
				line += fmt.Sprintf(" (`%s`)", c.Where)
			}
			fmt.Fprintln(w, line)
		}
		fmt.Fprintln(w, "\n</details>")
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// WriteJSON prints the report for tools.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func diffSets[T any](base, head []T, key func(T) string, emit func(T, string)) {
	bk, hk := map[string]bool{}, map[string]bool{}
	for _, x := range base {
		bk[key(x)] = true
	}
	for _, x := range head {
		hk[key(x)] = true
	}
	for _, x := range head {
		if !bk[key(x)] {
			emit(x, Added)
			bk[key(x)] = true // report duplicates once
		}
	}
	for _, x := range base {
		if !hk[key(x)] {
			emit(x, Removed)
			hk[key(x)] = true
		}
	}
}

func whereIf(op, where string) string {
	if op == Removed {
		return ""
	}
	return where
}

func firstEvidence(e []model.Evidence) string {
	if len(e) == 0 {
		return ""
	}
	return e[0].Pos.String()
}

func methodDiff(old, cur []string) string {
	o, c := map[string]bool{}, map[string]bool{}
	for _, m := range old {
		o[m] = true
	}
	for _, m := range cur {
		c[m] = true
	}
	var parts []string
	for _, m := range cur {
		if !o[m] {
			parts = append(parts, "+"+m)
		}
	}
	for _, m := range old {
		if !c[m] {
			parts = append(parts, "-"+m)
		}
	}
	if len(parts) == 0 {
		return "kind changed"
	}
	return strings.Join(parts, ", ")
}

func shortPkg(p string) string { return p[strings.LastIndex(p, "/")+1:] }

func componentsByID(m *model.Model) map[string]model.Component {
	out := map[string]model.Component{}
	for _, c := range m.Components {
		out[c.ID] = c
	}
	return out
}

// relPkg is the package path relative to the module, "." for the module root.
func relPkg(module, p string) string {
	if p == module {
		return "."
	}
	if module != "" && strings.HasPrefix(p, module+"/") {
		return p[len(module)+1:]
	}
	return p
}

func relPkgs(module string, ps []string) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = relPkg(module, p)
	}
	return out
}

func packageOwners(m *model.Model) map[string]string {
	out := map[string]string{}
	for _, c := range m.Components {
		for _, p := range c.Packages {
			out[p] = c.ID
		}
	}
	return out
}

func elementsByID(m *model.Model) map[string]model.CodeElement {
	out := map[string]model.CodeElement{}
	for _, e := range m.CodeElements {
		out[e.ID] = e
	}
	return out
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
