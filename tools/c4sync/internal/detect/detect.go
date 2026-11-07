// Package detect finds the external interfaces of components in Go source:
// HTTP routes, message topics and SQL tables. A value that is not a constant
// is reported as unresolved with the function it is in, not dropped.
package detect

import (
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"

	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/config"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/extract"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/model"
)

// httpCalls are the net/http registrations whose first argument is a pattern.
var httpCalls = []string{
	"net/http.ServeMux.Handle", "net/http.ServeMux.HandleFunc",
	"net/http.Handle", "net/http.HandleFunc",
}

// Run returns the interfaces found by the detectors switched on in cfg.
func Run(prog *extract.Program, m model.Mapping, cfg *config.Config) ([]model.ExternalInterface, error) {
	d := &detector{prog: prog, compOf: map[string]string{}, seen: map[string]bool{}}
	for c, ps := range m.Components {
		for _, p := range ps {
			d.compOf[p] = c
		}
	}
	var calls []callRule
	var fields []fieldRule
	if cfg.Detectors.HTTP {
		for _, s := range httpCalls {
			calls = append(calls, callRule{m: config.Matcher{Call: s}, kind: model.HTTP, dir: model.Serves})
		}
	}
	if t := cfg.Detectors.Topics; t != nil {
		for dir, ms := range map[string][]config.Matcher{model.Produces: t.Produce, model.Consumes: t.Consume} {
			for _, mt := range ms {
				if mt.Call != "" {
					calls = append(calls, callRule{m: mt, kind: model.Topic, dir: dir, element: t.Element})
				} else {
					fields = append(fields, fieldRule{m: mt, kind: model.Topic, dir: dir, element: t.Element})
				}
			}
		}
	}
	if s := cfg.Detectors.SQL; s != nil {
		d.ignore = s.Ignore
		for _, mt := range s.Calls {
			if mt.Call != "" {
				calls = append(calls, callRule{m: mt, kind: model.Table, element: s.Element})
			} else {
				fields = append(fields, fieldRule{m: mt, kind: model.Table, element: s.Element})
			}
		}
	}
	for _, p := range prog.Packages {
		comp := d.compOf[p.PkgPath]
		if comp == "" {
			continue
		}
		for _, f := range p.Syntax {
			d.file(p, f, comp, calls, fields)
		}
	}
	if s := cfg.Detectors.SQL; s != nil {
		for _, dir := range s.Migrations {
			if err := d.migrations(m.Module, dir, s.Element); err != nil {
				return nil, err
			}
		}
	}
	return d.out, nil
}

type callRule struct {
	m         config.Matcher
	kind, dir string
	element   string
}

type fieldRule struct {
	m         config.Matcher
	kind, dir string
	element   string
}

type detector struct {
	ignore []string
	prog   *extract.Program
	compOf map[string]string
	out    []model.ExternalInterface
	seen   map[string]bool
}

func (d *detector) add(i model.ExternalInterface) {
	key := strings.Join([]string{i.Component, i.Kind, i.Direction, i.Name, i.Where}, "\x00")
	if d.seen[key] {
		return
	}
	d.seen[key] = true
	d.out = append(d.out, i)
}

func (d *detector) file(p *packages.Package, f *ast.File, comp string, calls []callRule, fields []fieldRule) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			fn, ok := typeutil.Callee(p.TypesInfo, n).(*types.Func)
			if !ok {
				return true
			}
			sym := symbol(fn)
			for _, r := range calls {
				if !matchSymbol(r.m.Call, sym) {
					continue
				}
				for _, arg := range args(n, fn, r.m.Arg) {
					d.value(p, f, comp, r.kind, r.dir, r.element, arg)
				}
			}
		case *ast.CompositeLit:
			if len(fields) == 0 {
				return true
			}
			tv, ok := p.TypesInfo.Types[n]
			if !ok {
				return true
			}
			t := tv.Type
			if ptr, ok := t.(*types.Pointer); ok {
				t = ptr.Elem()
			}
			named, ok := types.Unalias(t).(*types.Named)
			if !ok || named.Obj().Pkg() == nil {
				return true
			}
			typ := named.Obj().Pkg().Path() + "." + named.Obj().Name()
			for _, el := range n.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				for _, r := range fields {
					if r.m.Field == typ+"."+key.Name {
						d.value(p, f, comp, r.kind, r.dir, r.element, kv.Value)
					}
				}
			}
		}
		return true
	})
}

// value records the interface for one expression: its constant value, or an
// unresolved entry when it is computed at runtime.
func (d *detector) value(p *packages.Package, f *ast.File, comp, kind, dir, element string, e ast.Expr) {
	base := model.ExternalInterface{Component: comp, Kind: kind, Direction: dir, Element: element, Pos: d.prog.Pos(e.Pos())}
	tv, ok := p.TypesInfo.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		base.Unresolved = true
		base.Where = extract.EnclosingFunc(f, e.Pos())
		if kind == model.Table {
			base.Direction = model.Reads
		}
		d.add(base)
		return
	}
	s := constant.StringVal(tv.Value)
	if kind != model.Table {
		base.Name = s
		d.add(base)
		return
	}
	for _, t := range Tables(s, d.ignore...) {
		i := base
		i.Name, i.Direction = t.Name, t.Direction
		d.add(i)
	}
}

// args returns the call arguments that hold the value.
func args(call *ast.CallExpr, fn *types.Func, idx int) []ast.Expr {
	if idx >= 0 {
		if idx < len(call.Args) {
			return call.Args[idx : idx+1]
		}
		return nil
	}
	sig := fn.Type().(*types.Signature)
	start := sig.Params().Len() - 1
	if start < 0 || start >= len(call.Args) {
		return nil
	}
	return call.Args[start:]
}

// symbol is "pkg/path.Func" or "pkg/path.Type.Method".
func symbol(fn *types.Func) string {
	if fn.Pkg() == nil {
		return fn.Name()
	}
	sig := fn.Type().(*types.Signature)
	if recv := sig.Recv(); recv != nil {
		t := recv.Type()
		if ptr, ok := t.(*types.Pointer); ok {
			t = ptr.Elem()
		}
		if named, ok := types.Unalias(t).(*types.Named); ok {
			return fn.Pkg().Path() + "." + named.Obj().Name() + "." + fn.Name()
		}
	}
	return fn.Pkg().Path() + "." + fn.Name()
}

func matchSymbol(pattern, sym string) bool {
	pp, prest, err := config.SplitSymbol(pattern)
	if err != nil {
		return false
	}
	sp, srest, err := config.SplitSymbol(sym)
	if err != nil || pp != sp {
		return false
	}
	pparts, sparts := strings.Split(prest, "."), strings.Split(srest, ".")
	if len(pparts) != len(sparts) {
		return false
	}
	for i := range pparts {
		if ok, _ := path.Match(pparts[i], sparts[i]); !ok {
			return false
		}
	}
	return true
}

// TableUse is one table in an SQL statement.
type TableUse struct {
	Name      string
	Direction string
}

var (
	reDefine = regexp.MustCompile(`(?i)\b(?:create\s+(?:unlogged\s+|temp(?:orary)?\s+)?table|alter\s+table)\s+(?:if\s+not\s+exists\s+|only\s+)?([a-z_][\w.]*)`)
	reWrite  = regexp.MustCompile(`(?i)\b(?:insert\s+into|update|delete\s+from|merge\s+into|copy)\s+([a-z_][\w.]*)`)
	reRead   = regexp.MustCompile(`(?i)\b(?:from|join)\s+(?:lateral\s+|only\s+)?([a-z_][\w.]*)(\s*\()?`)
	reFunc   = regexp.MustCompile(`(?i)\bcreate\s+(?:or\s+replace\s+)?(?:function|procedure)\s+([a-z_][\w.]*)`)
	notTable = map[string]bool{"select": true, "lateral": true, "only": true}
	// builtin names are never reported; config adds more with detectors.sql.ignore.
	builtin = []string{"pg_*", "information_schema.*", "unnest", "generate_series", "json_*", "jsonb_*", "rows"}
)

// Tables finds table names in an SQL text. It is a heuristic, not a parser:
// names after CREATE/ALTER TABLE define a table, after INSERT INTO, UPDATE,
// DELETE FROM, MERGE INTO and COPY write it, after FROM and JOIN read it.
// A name followed by "(" after FROM or JOIN is a set-returning function: it is
// reported as "name()" with the direction "calls"; CREATE FUNCTION defines one.
// Names matching ignore (path.Match patterns) or the built-in list are skipped.
func Tables(sql string, ignore ...string) []TableUse {
	seen := map[TableUse]bool{}
	var out []TableUse
	add := func(name, dir string) {
		name = strings.ToLower(name)
		bare := strings.TrimSuffix(name, "()")
		if notTable[bare] {
			return
		}
		for _, pat := range append(builtin, ignore...) {
			if ok, _ := path.Match(pat, bare); ok {
				return
			}
		}
		u := TableUse{name, dir}
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, m := range reDefine.FindAllStringSubmatch(sql, -1) {
		add(m[1], model.Defines)
	}
	for _, m := range reWrite.FindAllStringSubmatch(sql, -1) {
		add(m[1], model.Writes)
	}
	for _, m := range reFunc.FindAllStringSubmatch(sql, -1) {
		add(m[1]+"()", model.Defines)
	}
	for _, loc := range reRead.FindAllStringSubmatchIndex(sql, -1) {
		if loc[4] >= 0 { // followed by "(": a function
			add(sql[loc[2]:loc[3]]+"()", model.Calls)
			continue
		}
		before := strings.ToLower(sql[max(0, loc[0]-7):loc[0]])
		if strings.HasSuffix(strings.TrimSpace(before), "delete") {
			continue // DELETE FROM is a write, found above
		}
		add(sql[loc[2]:loc[3]], model.Reads)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Direction < out[j].Direction
	})
	return out
}

// migrations reads the .sql files of a folder: tables they create belong to
// the component of the folder's package.
func (d *detector) migrations(module, dir, element string) error {
	abs := filepath.Join(d.prog.Root, dir)
	pkg := path.Join(module, filepath.ToSlash(dir))
	comp := d.compOf[pkg]
	if comp == "" {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(abs, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(d.prog.Root, file)
		lines := strings.Split(string(raw), "\n")
		for n, line := range lines {
			for _, t := range Tables(line, d.ignore...) {
				if t.Direction != model.Defines {
					continue
				}
				d.add(model.ExternalInterface{
					Component: comp, Kind: model.Table, Direction: model.Defines, Name: t.Name,
					Element: element, Pos: model.Pos{File: filepath.ToSlash(rel), Line: n + 1},
				})
			}
		}
	}
	return nil
}
