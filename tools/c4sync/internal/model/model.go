// Package model is the language-neutral structure that extractors fill and writers read.
package model

import (
	"fmt"
	"sort"
)

// Mapping assigns import paths of packages to component ids.
// Packages without a component are listed in Unmapped.
type Mapping struct {
	Module     string
	Components map[string][]string // component id -> package import paths
	Unmapped   []string            // package import paths
}

// ComponentOf returns the component of a package, or "".
func (m Mapping) ComponentOf(pkg string) string {
	for c, pkgs := range m.Components {
		for _, p := range pkgs {
			if p == pkg {
				return c
			}
		}
	}
	return ""
}

// Normalize sorts everything so that output is deterministic.
func (m *Mapping) Normalize() {
	for c := range m.Components {
		sort.Strings(m.Components[c])
	}
	sort.Strings(m.Unmapped)
}

// Pos is a source location, relative to the project root.
type Pos struct {
	File string
	Line int
}

func (p Pos) String() string { return fmt.Sprintf("%s:%d", p.File, p.Line) }

// Component is one C3 component: a group of packages.
type Component struct {
	ID          string
	Name        string
	Description string
	Technology  string
	Tags        []string
	Packages    []string
}

// Relation kinds.
const (
	Imports    = "imports"
	Implements = "implements"
	Embeds     = "embeds"
	Uses       = "uses"
)

// Evidence is one place in the code that makes a relation.
type Evidence struct {
	Pos    Pos
	Detail string // for imports: "from -> to" package paths
}

// Relation is a dependency of one component on another (C3).
type Relation struct {
	From, To string // component ids
	Kind     string
	Evidence []Evidence
}

// ExternalRelation is a dependency of a component on an element outside the
// container, such as a library mapped to a workspace element.
type ExternalRelation struct {
	From        string // component id
	To          string // workspace identifier
	Description string
	Technology  string
	Evidence    []Evidence
}

// Element kinds for code elements.
const (
	Struct    = "struct"
	Interface = "interface"
	FuncType  = "func"
	OtherType = "type"
)

// CodeElement is a named Go type inside a component (code level).
type CodeElement struct {
	ID        string // stable, unique in the model
	Component string
	Package   string
	Name      string // with type parameters, e.g. "Page[T]"
	Kind      string
	Exported  bool
	Methods   []string // "Name(params) results", sorted
	Pos       Pos
}

// CodeRelation is a relation between two code elements.
type CodeRelation struct {
	From, To string // code element ids
	Kind     string // implements, embeds, uses
	Evidence []Evidence
}

// Interface kinds and directions found by detectors.
const (
	HTTP  = "http"
	Topic = "topic"
	Table = "table"

	Serves   = "serves"
	Produces = "produces"
	Consumes = "consumes"
	Reads    = "reads"
	Writes   = "writes"
	Defines  = "defines"
	Calls    = "calls" // an SQL function
)

// ExternalInterface is something a component exposes or talks to:
// an HTTP route, a message topic, an SQL table.
type ExternalInterface struct {
	Component  string
	Kind       string
	Direction  string
	Name       string // empty when Unresolved
	Unresolved bool   // the value is built at runtime
	Where      string // enclosing function, stable across edits; set for unresolved values
	Element    string // workspace identifier to draw a relation to, if any
	Pos        Pos
}

// Model is what writers render.
type Model struct {
	Container         string // name of the service, used in generated workspaces
	Module            string // Go module path
	Components        []Component
	Relations         []Relation
	ExternalRelations []ExternalRelation
	CodeElements      []CodeElement
	CodeRelations     []CodeRelation
	Interfaces        []ExternalInterface
	Unmapped          []string
}

// Component returns the component with the id, or nil.
func (m *Model) Component(id string) *Component {
	for i := range m.Components {
		if m.Components[i].ID == id {
			return &m.Components[i]
		}
	}
	return nil
}

// Sort orders everything so that writers are deterministic.
func (m *Model) Sort() {
	sort.Slice(m.Components, func(i, j int) bool { return m.Components[i].ID < m.Components[j].ID })
	for i := range m.Components {
		sort.Strings(m.Components[i].Packages)
	}
	sort.Slice(m.Relations, func(i, j int) bool {
		a, b := m.Relations[i], m.Relations[j]
		return less(a.From, b.From, a.To, b.To, a.Kind, b.Kind)
	})
	for i := range m.Relations {
		sortEvidence(m.Relations[i].Evidence)
	}
	sort.Slice(m.ExternalRelations, func(i, j int) bool {
		a, b := m.ExternalRelations[i], m.ExternalRelations[j]
		return less(a.From, b.From, a.To, b.To, a.Description, b.Description)
	})
	for i := range m.ExternalRelations {
		sortEvidence(m.ExternalRelations[i].Evidence)
	}
	sort.Slice(m.CodeElements, func(i, j int) bool { return m.CodeElements[i].ID < m.CodeElements[j].ID })
	sort.Slice(m.CodeRelations, func(i, j int) bool {
		a, b := m.CodeRelations[i], m.CodeRelations[j]
		return less(a.From, b.From, a.To, b.To, a.Kind, b.Kind)
	})
	for i := range m.CodeRelations {
		sortEvidence(m.CodeRelations[i].Evidence)
	}
	sort.Slice(m.Interfaces, func(i, j int) bool {
		a, b := m.Interfaces[i], m.Interfaces[j]
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Direction != b.Direction {
			return a.Direction < b.Direction
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Where < b.Where
	})
	sort.Strings(m.Unmapped)
}

func less(a1, b1, a2, b2, a3, b3 string) bool {
	if a1 != b1 {
		return a1 < b1
	}
	if a2 != b2 {
		return a2 < b2
	}
	return a3 < b3
}

func sortEvidence(e []Evidence) {
	sort.Slice(e, func(i, j int) bool {
		if e[i].Pos.File != e[j].Pos.File {
			return e[i].Pos.File < e[j].Pos.File
		}
		if e[i].Pos.Line != e[j].Pos.Line {
			return e[i].Pos.Line < e[j].Pos.Line
		}
		return e[i].Detail < e[j].Detail
	})
}
