// Package pipeline builds the model of one service and renders its outputs:
// go-arch-lint mapping, Go extraction, detectors, DSL writers.
package pipeline

import (
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
	r := &Result{Model: m, Mapping: mapping, Files: map[string]string{}}
	o := Options(cfg)
	if p := cfg.Output.Components; p != "" {
		r.Files[cfg.Path(p)] = dsl.Components(&r.Model, o)
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
