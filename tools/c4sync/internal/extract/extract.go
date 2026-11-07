// Package extract fills the model from Go source with go/packages and go/types:
// relations between components (C3) and code elements with their relations
// (code level). Which package belongs to which component comes from the mapping.
package extract

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/config"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/model"
)

// Program is the loaded source of one module.
type Program struct {
	Root     string // absolute module root
	Module   string
	Fset     *token.FileSet
	Packages []*packages.Package // sorted by import path, no test packages
}

// Load loads every package of the module at root, without test files.
func Load(root string) (*Program, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Dir: root,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, err
	}
	var errs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			errs = append(errs, e.Error())
		}
	})
	if len(errs) > 0 {
		return nil, fmt.Errorf("load %s: %s", root, strings.Join(errs, "; "))
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].PkgPath < pkgs[j].PkgPath })
	p := &Program{Root: root, Fset: cfg.Fset, Packages: pkgs}
	if len(pkgs) > 0 && pkgs[0].Module != nil {
		p.Module = pkgs[0].Module.Path
	}
	if p.Fset == nil && len(pkgs) > 0 {
		p.Fset = pkgs[0].Fset
	}
	return p, nil
}

// Pos turns a token position into a position relative to the module root.
func (p *Program) Pos(pos token.Pos) model.Pos {
	position := p.Fset.Position(pos)
	file := position.Filename
	if rel, err := filepath.Rel(p.Root, file); err == nil {
		file = filepath.ToSlash(rel)
	}
	return model.Pos{File: file, Line: position.Line}
}

// Build turns the program and the mapping into a model: components with the
// metadata from the config, relations and code elements.
func Build(prog *Program, m model.Mapping, cfg *config.Config) model.Model {
	out := model.Model{Container: cfg.Name, Module: m.Module, Unmapped: append([]string(nil), m.Unmapped...)}
	compOf := map[string]string{}
	for c, ps := range m.Components {
		for _, p := range ps {
			compOf[p] = c
		}
		md := cfg.Components[c]
		name := md.Name
		if name == "" {
			name = c
		}
		out.Components = append(out.Components, model.Component{
			ID: c, Name: name, Description: md.Description, Technology: md.Technology,
			Tags: md.Tags, Packages: append([]string(nil), ps...),
		})
	}
	b := &builder{prog: prog, cfg: cfg, compOf: compOf, module: m.Module, elems: map[*types.TypeName]*model.CodeElement{}}
	b.imports(&out)
	b.codeElements(&out)
	b.codeRelations(&out)
	out.Sort()
	return out
}

type builder struct {
	prog   *Program
	cfg    *config.Config
	compOf map[string]string
	module string
	elems  map[*types.TypeName]*model.CodeElement
	order  []*types.TypeName
}

func (b *builder) imports(out *model.Model) {
	rels := map[[3]string]*model.Relation{}
	exts := map[[4]string]*model.ExternalRelation{}
	for _, p := range b.prog.Packages {
		from := b.compOf[p.PkgPath]
		if from == "" {
			continue
		}
		for _, f := range p.Syntax {
			for _, spec := range f.Imports {
				imp, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					continue
				}
				ev := model.Evidence{Pos: b.prog.Pos(spec.Pos()), Detail: p.PkgPath + " -> " + imp}
				if to := b.compOf[imp]; to != "" {
					if to == from {
						continue
					}
					k := [3]string{from, to, model.Imports}
					if rels[k] == nil {
						rels[k] = &model.Relation{From: from, To: to, Kind: model.Imports}
					}
					rels[k].Evidence = append(rels[k].Evidence, ev)
					continue
				}
				for _, e := range b.cfg.Externals {
					if imp != e.Import && !strings.HasPrefix(imp, e.Import+"/") {
						continue
					}
					desc, tech := e.Description, e.Technology
					if desc == "" {
						desc = "Uses"
					}
					if tech == "" {
						tech = e.Import
					}
					k := [4]string{from, e.Element, desc, tech}
					if exts[k] == nil {
						exts[k] = &model.ExternalRelation{From: from, To: e.Element, Description: desc, Technology: tech}
					}
					exts[k].Evidence = append(exts[k].Evidence, ev)
				}
			}
		}
	}
	for _, r := range rels {
		out.Relations = append(out.Relations, *r)
	}
	for _, r := range exts {
		out.ExternalRelations = append(out.ExternalRelations, *r)
	}
}

func (b *builder) codeElements(out *model.Model) {
	for _, p := range b.prog.Packages {
		comp := b.compOf[p.PkgPath]
		if comp == "" || p.Types == nil {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			e := &model.CodeElement{
				ID:        b.elementID(p.PkgPath, name),
				Component: comp,
				Package:   p.PkgPath,
				Name:      displayName(named),
				Kind:      kindOf(named),
				Exported:  tn.Exported(),
				Methods:   b.methods(named, p.Types),
				Pos:       b.prog.Pos(tn.Pos()),
			}
			b.elems[tn] = e
			b.order = append(b.order, tn)
		}
	}
	for _, tn := range b.order {
		out.CodeElements = append(out.CodeElements, *b.elems[tn])
	}
}

func (b *builder) elementID(pkg, name string) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(pkg, b.module), "/")
	if rel == "" {
		rel = "root"
	}
	return rel + "." + name
}

func displayName(n *types.Named) string {
	tp := n.TypeParams()
	if tp.Len() == 0 {
		return n.Obj().Name()
	}
	names := make([]string, tp.Len())
	for i := range names {
		names[i] = tp.At(i).Obj().Name()
	}
	return n.Obj().Name() + "[" + strings.Join(names, ", ") + "]"
}

func kindOf(n *types.Named) string {
	switch n.Underlying().(type) {
	case *types.Struct:
		return model.Struct
	case *types.Interface:
		return model.Interface
	case *types.Signature:
		return model.FuncType
	}
	return model.OtherType
}

// methods lists the exported methods (all of them for an interface), or every
// declared method when none is exported.
func (b *builder) methods(n *types.Named, pkg *types.Package) []string {
	qual := func(p *types.Package) string {
		if p == pkg {
			return ""
		}
		return p.Name()
	}
	var fns []*types.Func
	if iface, ok := n.Underlying().(*types.Interface); ok {
		for i := 0; i < iface.NumMethods(); i++ {
			fns = append(fns, iface.Method(i))
		}
	} else {
		for i := 0; i < n.NumMethods(); i++ {
			fns = append(fns, n.Method(i))
		}
		var exported []*types.Func
		for _, f := range fns {
			if f.Exported() {
				exported = append(exported, f)
			}
		}
		if len(exported) > 0 {
			fns = exported
		}
	}
	var out []string
	for _, f := range fns {
		sig := f.Type().(*types.Signature)
		out = append(out, f.Name()+"("+tupleTypes(sig.Params(), sig.Variadic(), qual)+")"+results(sig.Results(), qual))
	}
	sort.Strings(out)
	return out
}

func tupleTypes(t *types.Tuple, variadic bool, q types.Qualifier) string {
	parts := make([]string, t.Len())
	for i := range parts {
		typ := t.At(i).Type()
		if variadic && i == t.Len()-1 {
			if s, ok := typ.(*types.Slice); ok {
				parts[i] = "..." + types.TypeString(s.Elem(), q)
				continue
			}
		}
		parts[i] = types.TypeString(typ, q)
	}
	return strings.Join(parts, ", ")
}

func results(t *types.Tuple, q types.Qualifier) string {
	switch t.Len() {
	case 0:
		return ""
	case 1:
		return " " + types.TypeString(t.At(0).Type(), q)
	}
	return " (" + tupleTypes(t, false, q) + ")"
}

func (b *builder) codeRelations(out *model.Model) {
	rels := map[[3]string]*model.CodeRelation{}
	add := func(from *types.TypeName, to types.Type, kind string, pos token.Pos) {
		src := b.elems[from]
		for _, tn := range namedIn(to) {
			dst := b.elems[tn]
			if dst == nil || dst == src {
				continue
			}
			k := [3]string{src.ID, dst.ID, kind}
			if rels[k] == nil {
				rels[k] = &model.CodeRelation{From: src.ID, To: dst.ID, Kind: kind}
			}
			rels[k].Evidence = append(rels[k].Evidence, model.Evidence{Pos: b.prog.Pos(pos)})
		}
	}
	addSig := func(from *types.TypeName, sig *types.Signature, pos token.Pos) {
		for _, t := range []*types.Tuple{sig.Params(), sig.Results()} {
			for i := 0; i < t.Len(); i++ {
				add(from, t.At(i).Type(), model.Uses, pos)
			}
		}
	}
	var concrete, ifaces []*types.TypeName
	for _, tn := range b.order {
		named := tn.Type().(*types.Named)
		switch u := named.Underlying().(type) {
		case *types.Struct:
			for i := 0; i < u.NumFields(); i++ {
				f := u.Field(i)
				kind := model.Uses
				if f.Embedded() {
					kind = model.Embeds
				}
				add(tn, f.Type(), kind, f.Pos())
			}
		case *types.Interface:
			for i := 0; i < u.NumEmbeddeds(); i++ {
				add(tn, u.EmbeddedType(i), model.Embeds, tn.Pos())
			}
			for i := 0; i < u.NumExplicitMethods(); i++ {
				m := u.ExplicitMethod(i)
				addSig(tn, m.Type().(*types.Signature), m.Pos())
			}
		case *types.Signature:
			addSig(tn, u, tn.Pos())
		}
		if _, isIface := named.Underlying().(*types.Interface); !isIface {
			for i := 0; i < named.NumMethods(); i++ {
				m := named.Method(i)
				addSig(tn, m.Type().(*types.Signature), m.Pos())
			}
		}
		if named.TypeParams().Len() > 0 {
			continue // generic types are not checked for implements
		}
		if iface, ok := named.Underlying().(*types.Interface); ok {
			if iface.NumMethods() > 0 {
				ifaces = append(ifaces, tn)
			}
		} else {
			concrete = append(concrete, tn)
		}
	}
	for _, c := range concrete {
		for _, i := range ifaces {
			iface := i.Type().Underlying().(*types.Interface)
			if types.Implements(c.Type(), iface) || types.Implements(types.NewPointer(c.Type()), iface) {
				add(c, i.Type(), model.Implements, c.Pos())
			}
		}
	}
	for _, r := range rels {
		out.CodeRelations = append(out.CodeRelations, *r)
	}
}

// namedIn returns the declared types a type expression refers to: through
// pointers, slices, arrays, maps, channels, function types and type arguments.
func namedIn(t types.Type) []*types.TypeName {
	var out []*types.TypeName
	seen := map[types.Type]bool{}
	var walk func(types.Type)
	walk = func(t types.Type) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		switch t := t.(type) {
		case *types.Named:
			out = append(out, t.Origin().Obj())
			for i := 0; i < t.TypeArgs().Len(); i++ {
				walk(t.TypeArgs().At(i))
			}
		case *types.Alias:
			walk(types.Unalias(t))
		case *types.Pointer:
			walk(t.Elem())
		case *types.Slice:
			walk(t.Elem())
		case *types.Array:
			walk(t.Elem())
		case *types.Map:
			walk(t.Key())
			walk(t.Elem())
		case *types.Chan:
			walk(t.Elem())
		case *types.Signature:
			for _, tup := range []*types.Tuple{t.Params(), t.Results()} {
				for i := 0; i < tup.Len(); i++ {
					walk(tup.At(i).Type())
				}
			}
		}
	}
	walk(t)
	return out
}

// EnclosingFunc returns "Func" or "Type.Method" for the declaration that holds pos.
func EnclosingFunc(f *ast.File, pos token.Pos) string {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || pos < fd.Pos() || pos > fd.End() {
			continue
		}
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			return recvName(fd.Recv.List[0].Type) + "." + fd.Name.Name
		}
		return fd.Name.Name
	}
	return "package level"
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

// Reachable returns the module packages that the package at rel (relative to
// the module root, e.g. "cmd/writer") imports directly or transitively,
// including itself.
func (p *Program) Reachable(rel string) (map[string]bool, error) {
	byPath := map[string]*packages.Package{}
	for _, pkg := range p.Packages {
		byPath[pkg.PkgPath] = pkg
	}
	start := p.Module
	if rel = strings.Trim(filepath.ToSlash(rel), "/"); rel != "" && rel != "." {
		start = p.Module + "/" + rel
	}
	if byPath[start] == nil {
		return nil, fmt.Errorf("main package %s not found in %s", start, p.Root)
	}
	out := map[string]bool{}
	var walk func(string)
	walk = func(path string) {
		pkg := byPath[path]
		if pkg == nil || out[path] {
			return
		}
		out[path] = true
		for imp := range pkg.Imports {
			walk(imp)
		}
	}
	walk(start)
	return out, nil
}
