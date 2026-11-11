// Package check finds what makes the committed diagrams or the code wrong:
// stale generated files, packages outside every component, and imports that
// go-arch-lint rules forbid.
package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/archlint"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/config"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/pipeline"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/textdiff"
)

// Exit codes of `c4sync check`. Stale and violations combine: 3 means both.
const (
	OK         = 0
	Violations = 1
	Stale      = 2
	Error      = 4
)

// Report is the result of a check.
type Report struct {
	Stale      []StaleFile          `json:"stale"`
	Unmapped   []string             `json:"unmapped"`
	Violations []archlint.Violation `json:"violations"`
}

// StaleFile is a generated file that differs from what the code gives now.
type StaleFile struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
}

// Run builds the outputs in memory and compares them with the files on disk.
func Run(cfg *config.Config, lint archlint.Runner) (*Report, error) {
	res, err := pipeline.Build(cfg, lint)
	if err != nil {
		return nil, err
	}
	r := &Report{Unmapped: res.Mapping.Unmapped}
	paths := make([]string, 0, len(res.Files))
	for p := range res.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		want := res.Files[p]
		got, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if string(got) != want {
			name := display(cfg, p)
			r.Stale = append(r.Stale, StaleFile{Path: name, Diff: textdiff.Unified(name+" (committed)", name+" (from code)", string(got), want, 2)})
		}
	}
	vs, err := lint.Check(cfg.Project(), cfg.Arch())
	if err != nil {
		return nil, err
	}
	r.Violations = vs
	return r, nil
}

// Code is the exit code for the report.
func (r *Report) Code() int {
	code := OK
	if len(r.Unmapped) > 0 || len(r.Violations) > 0 {
		code |= Violations
	}
	if len(r.Stale) > 0 {
		code |= Stale
	}
	return code
}

// WriteText prints the report for people. An empty report prints nothing.
func (r *Report) WriteText(w io.Writer) {
	for _, s := range r.Stale {
		fmt.Fprintf(w, "stale: %s: regenerate with `c4sync generate`\n%s", s.Path, s.Diff)
	}
	for _, p := range r.Unmapped {
		fmt.Fprintf(w, "unmapped package: %s: add it to a component in the go-arch-lint config, or exclude it there\n", p)
	}
	for _, v := range r.Violations {
		if v.Pos.File != "" {
			fmt.Fprintf(w, "%s: %s\n", v.Pos, v.Message)
		} else {
			fmt.Fprintln(w, v.Message)
		}
	}
}

// WriteJSON prints the report for tools.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func display(cfg *config.Config, p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	_ = cfg
	return p
}
