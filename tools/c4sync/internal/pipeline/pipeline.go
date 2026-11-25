// Package pipeline builds the model of one service and renders its outputs:
// go-arch-lint mapping, Go extraction, detectors, DSL writers.
package pipeline

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/archlint"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/config"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/detect"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/dsl"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/extract"
	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/model"
)

// Result is the model and the generated files, keyed by absolute path.
type Result struct {
	Model   model.Model
	Mapping model.Mapping
	Files   map[string]string
	// Containers are the per-binary models, by container name.
	Containers map[string]model.Model
}

// Build runs everything for one config.
func Build(cfg *config.Config, lint archlint.Runner) (*Result, error) {
	mapping, err := lint.Mapping(cfg.Project(), cfg.Arch())
	if err != nil {
		return nil, err
	}
	known, err := config.ArchComponents(cfg.Arch())
	if err != nil {
		return nil, err
	}
	for c := range mapping.Components {
		known[c] = true
	}
	if err := cfg.CheckComponents(known); err != nil {
		return nil, err
	}
	prog, err := extract.Load(cfg.Project())
	if err != nil {
		return nil, err
	}
	m := extract.Build(prog, mapping, cfg)
	ifs, err := detect.Run(prog, mapping, cfg)
	if err != nil {
		return nil, err
	}
	m.Interfaces = ifs
	m.Sort()
	r := &Result{Model: m, Mapping: mapping, Files: map[string]string{}, Containers: map[string]model.Model{}}
	o := Options(cfg)
	if p := cfg.Output.Components; p != "" {
		r.Files[cfg.Path(p)] = dsl.Components(&r.Model, o)
	}
	for _, ct := range cfg.Containers {
		pkgs, err := prog.Reachable(ct.Main)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", ct.Name, err)
		}
		cm := r.Model.Restrict(ct.Name, pkgs)
		r.Containers[ct.Name] = cm
		co := o
		co.Prefix = ct.IdentifierPrefix
		if co.Prefix == "" {
			co.Prefix = o.Prefix + "_" + strings.ToLower(sanitize(ct.Name))
		}
		r.Files[cfg.Path(ct.Output)] = dsl.Components(&cm, co)
	}
	if p := cfg.Output.Code; p != "" {
		r.Files[cfg.Path(p)] = dsl.Code(&r.Model, o)
	}
	return r, nil
}

// Options are the writer options from the config.
func Options(cfg *config.Config) dsl.Options {
	return dsl.Options{Prefix: cfg.IdentifierPrefix, MaxElements: cfg.Code.MaxElements, MaxMethods: cfg.Code.MaxMethods}
}

// sanitize keeps letters, digits and underscores, for DSL identifiers.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return '_'
	}, s)
}
