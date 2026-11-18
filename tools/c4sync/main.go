// Command c4sync keeps C4 diagrams in sync with Go code. It reads the
// go-arch-lint config (which packages form a component) and c4sync.yml
// (C4 metadata, detectors, outputs) and writes Structurizr DSL:
//
//	c4sync generate  writes the C3 fragment and the code-level workspace
//	c4sync check     fails when they are stale, a package has no component,
//	                 or go-arch-lint rules are broken
//	c4sync diff      reports structural changes between two source trees
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/archlint"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/check"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/config"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/diff"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/dsl"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/model"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/pipeline"
)

const usage = `usage:
  c4sync generate [-config c4sync.yml]
  c4sync check    [-config c4sync.yml] [-json]
  c4sync diff     [-config c4sync.yml] -base-config <path> [-important a,b] [-json file] [-views file]

go-arch-lint is taken from -go-arch-lint, $C4SYNC_GO_ARCH_LINT or PATH.
check exit codes: 0 ok, 1 violations, 2 stale output, 3 both, 4 error.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return check.Error
	}
	var err error
	code := 0
	switch args[0] {
	case "generate":
		err = generate(args[1:], stdout)
	case "check":
		code, err = runCheck(args[1:], stdout)
	case "diff":
		err = runDiff(args[1:], stdout)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		err = fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
	if err != nil {
		fmt.Fprintln(stderr, "c4sync:", err)
		return check.Error
	}
	return code
}

type common struct {
	config string
	lint   string
}

func flags(name string, c *common) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.StringVar(&c.config, "config", "c4sync.yml", "c4sync config")
	def := os.Getenv("C4SYNC_GO_ARCH_LINT")
	if def == "" {
		def = "go-arch-lint"
	}
	set.StringVar(&c.lint, "go-arch-lint", def, "go-arch-lint binary")
	return set
}

func generate(args []string, stdout io.Writer) error {
	var c common
	if err := flags("generate", &c).Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(c.config)
	if err != nil {
		return err
	}
	res, err := pipeline.Build(cfg, archlint.Runner{Bin: c.lint})
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(res.Files))
	for p := range res.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(res.Files[p]), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "wrote", p)
	}
	for _, u := range res.Mapping.Unmapped {
		fmt.Fprintln(stdout, "unmapped package:", u)
	}
	return nil
}

func runCheck(args []string, stdout io.Writer) (int, error) {
	var c common
	set := flags("check", &c)
	asJSON := set.Bool("json", false, "print the report as JSON")
	if err := set.Parse(args); err != nil {
		return 0, err
	}
	cfg, err := config.Load(c.config)
	if err != nil {
		return 0, err
	}
	r, err := check.Run(cfg, archlint.Runner{Bin: c.lint})
	if err != nil {
		return 0, err
	}
	if *asJSON {
		if err := r.WriteJSON(stdout); err != nil {
			return 0, err
		}
	} else {
		r.WriteText(stdout)
	}
	return r.Code(), nil
}

func runDiff(args []string, stdout io.Writer) error {
	var c common
	set := flags("diff", &c)
	baseConfig := set.String("base-config", "", "c4sync config in the base tree; a missing file means an empty base")
	important := set.String("important", "", "comma-separated components listed first, added to the config's list")
	jsonOut := set.String("json", "", "also write the report as JSON to this file")
	viewsOut := set.String("views", "", "write a DSL workspace with views of the changed components to this file")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *baseConfig == "" {
		return errors.New("diff: -base-config is required")
	}
	lint := archlint.Runner{Bin: c.lint}
	cfg, err := config.Load(c.config)
	if err != nil {
		return err
	}
	head, err := pipeline.Build(cfg, lint)
	if err != nil {
		return err
	}
	base := &model.Model{Container: head.Model.Container}
	if _, err := os.Stat(*baseConfig); err == nil {
		bcfg, err := config.Load(*baseConfig)
		if err != nil {
			return fmt.Errorf("base: %w", err)
		}
		bres, err := pipeline.Build(bcfg, lint)
		if err != nil {
			return fmt.Errorf("base: %w", err)
		}
		base = &bres.Model
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	imp := append([]string(nil), cfg.Important...)
	for _, s := range strings.Split(*important, ",") {
		if s = strings.TrimSpace(s); s != "" {
			imp = append(imp, s)
		}
	}
	r := diff.Compare(base, &head.Model, imp)
	r.WriteMarkdown(stdout)
	if *jsonOut != "" {
		f, err := os.Create(*jsonOut)
		if err != nil {
			return err
		}
		if err := r.WriteJSON(f); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	if *viewsOut != "" && !r.Empty() {
		comps := r.Components(&head.Model)
		if len(comps) > 0 {
			out := dsl.Changes(&head.Model, comps, pipeline.Options(cfg))
			if err := os.WriteFile(*viewsOut, []byte(out), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
