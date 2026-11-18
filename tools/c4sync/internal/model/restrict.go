package model

import (
	"path"
	"strings"
)

// Restrict returns the part of the model that one binary is built from: the
// components with a package in pkgs (import paths), and only the relations,
// interfaces and code that come from those packages.
func (m *Model) Restrict(container string, pkgs map[string]bool) Model {
	out := Model{Container: container, Module: m.Module}
	keep := map[string]bool{}
	for _, c := range m.Components {
		var ps []string
		for _, p := range c.Packages {
			if pkgs[p] {
				ps = append(ps, p)
			}
		}
		if len(ps) == 0 {
			continue
		}
		c.Packages = ps
		keep[c.ID] = true
		out.Components = append(out.Components, c)
	}
	fromKept := func(ev []Evidence) []Evidence {
		var out []Evidence
		for _, e := range ev {
			from, _, _ := strings.Cut(e.Detail, " -> ")
			if pkgs[from] {
				out = append(out, e)
			}
		}
		return out
	}
	for _, r := range m.Relations {
		if !keep[r.From] || !keep[r.To] {
			continue
		}
		if r.Evidence = fromKept(r.Evidence); len(r.Evidence) > 0 {
			out.Relations = append(out.Relations, r)
		}
	}
	for _, r := range m.ExternalRelations {
		if !keep[r.From] {
			continue
		}
		if r.Evidence = fromKept(r.Evidence); len(r.Evidence) > 0 {
			out.ExternalRelations = append(out.ExternalRelations, r)
		}
	}
	for _, i := range m.Interfaces {
		if keep[i.Component] && pkgs[m.packageOfFile(i.Pos.File)] {
			out.Interfaces = append(out.Interfaces, i)
		}
	}
	elems := map[string]bool{}
	for _, e := range m.CodeElements {
		if keep[e.Component] && pkgs[e.Package] {
			elems[e.ID] = true
			out.CodeElements = append(out.CodeElements, e)
		}
	}
	for _, r := range m.CodeRelations {
		if elems[r.From] && elems[r.To] {
			out.CodeRelations = append(out.CodeRelations, r)
		}
	}
	return out
}

// packageOfFile is the import path of the folder of a module-relative file.
func (m *Model) packageOfFile(file string) string {
	dir := path.Dir(file)
	if dir == "." {
		return m.Module
	}
	return m.Module + "/" + dir
}
