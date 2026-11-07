package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Feature is one docs/features/<slug>.yaml file. The format is described in
// docs/features/README.md.
type Feature struct {
	Feature           string              `yaml:"feature"`
	Title             string              `yaml:"title"`
	Stage             string              `yaml:"stage"`
	Critical          bool                `yaml:"critical"`
	Flag              string              `yaml:"flag"`
	Code              []string            `yaml:"code"`
	C4                []string            `yaml:"c4"`
	Links             Links               `yaml:"links"`
	Requirements      []string            `yaml:"requirements"`
	Acceptance        map[string]AC       `yaml:"acceptance"`
	Checklist         map[string]Item     `yaml:"checklist"`
	QualityScenarios  QualityScenarios    `yaml:"quality_scenarios"`
	PlanApproved      string              `yaml:"plan_approved"`
	StructureApproved string              `yaml:"structure_approved"`
	Reviews           []Review            `yaml:"reviews"`
	Deviations        []map[string]string `yaml:"deviations"`
	Runs              []Run               `yaml:"runs"`

	file string // path relative to the repo root
}

type Links struct {
	Issues []string `yaml:"issues"`
}

type AC struct {
	Text     string   `yaml:"text"`
	Level    string   `yaml:"level"`
	Planned  string   `yaml:"planned"`
	Verified Verified `yaml:"verified"`
}

type Item struct {
	Planned  string   `yaml:"planned"`
	Verified Verified `yaml:"verified"`
}

type QualityScenarios struct {
	Touched         []string        `yaml:"touched"`
	Tests           map[string]Item `yaml:"tests"`
	RecommendedRuns []string        `yaml:"recommended_runs"`
}

type Review struct {
	Date   string `yaml:"date"`
	Commit string `yaml:"commit"`
	Scope  string `yaml:"scope"`
}

type Run struct {
	Date         string   `yaml:"date"`
	Commit       string   `yaml:"commit"`
	StageChecked string   `yaml:"stage_checked"`
	Verdict      string   `yaml:"verdict"`
	Failed       []string `yaml:"failed"`
	NotChecked   []string `yaml:"not_checked"`
}

// Verified is either a list of tests ("path:TestName") or a string that
// starts with "n/a" and gives the reason.
type Verified struct {
	Tests []string
	NA    string
}

func (v *Verified) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			return nil
		}
		if !strings.HasPrefix(n.Value, "n/a") {
			return fmt.Errorf("line %d: verified must be a list of tests or \"n/a — <reason>\"", n.Line)
		}
		v.NA = n.Value
		return nil
	case yaml.SequenceNode:
		return n.Decode(&v.Tests)
	}
	return fmt.Errorf("line %d: verified must be a list of tests or \"n/a — <reason>\"", n.Line)
}

var stages = map[string]int{"proto": 1, "alpha": 2, "beta": 3, "stable": 4}

var (
	slugRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	acRe     = regexp.MustCompile(`^AC-[1-9][0-9]*$`)
	itemRe   = regexp.MustCompile(`^T-(0[1-9]|1[0-2])$`)
	qsRe     = regexp.MustCompile(`^QS-[0-9]{2}$`)
	reqRe    = regexp.MustCompile(`^(FR|QS|C)-[0-9]{2}$`)
	issueRe  = regexp.MustCompile(`^HAMSP-[1-9][0-9]*$`)
	dateRe   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	commitRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	levels   = map[string]bool{"unit": true, "integration": true, "contract": true, "e2e": true, "k6": true, "chaos": true, "manual": true}
)

// loadFeatures reads every *.yaml file in dir, sorted by slug.
func loadFeatures(root, dir string) ([]*Feature, error) {
	paths, err := filepath.Glob(filepath.Join(root, dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	stem := func(p string) string { return strings.TrimSuffix(filepath.Base(p), ".yaml") }
	sort.Slice(paths, func(i, j int) bool { return stem(paths[i]) < stem(paths[j]) })
	var out []*Feature
	var errs []error
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(root, p)
		f := &Feature{file: filepath.ToSlash(rel)}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(f); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.file, err))
			continue
		}
		out = append(out, f)
	}
	return out, errors.Join(errs...)
}

// validate checks one feature file on its own. Checks that need the
// repository (globs, tests) are in checkRepo.
func (f *Feature) validate() []error {
	var errs []error
	bad := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", f.file, fmt.Sprintf(format, a...)))
	}
	slug := strings.TrimSuffix(filepath.Base(f.file), ".yaml")
	if !slugRe.MatchString(slug) {
		bad("file name %q is not a kebab-case slug", slug)
	}
	if f.Feature != slug {
		bad("feature %q must equal the file name %q", f.Feature, slug)
	}
	if f.Title == "" {
		bad("title is missing")
	}
	if _, ok := stages[f.Stage]; f.Stage != "" && !ok {
		bad("stage %q must be proto, alpha, beta or stable", f.Stage)
	} else if f.Stage != "" && stages[f.Stage] > f.highestReady() {
		bad("stage %s is not backed by a ready verify run for %s or higher", f.Stage, f.Stage)
	}
	if f.Stage == "stable" && len(f.Reviews) == 0 {
		bad("stage is stable but reviews is empty (gate G-06)")
	}
	if stages[f.Stage] >= stages["alpha"] && len(f.Code) == 0 {
		bad("stage %s needs code globs", f.Stage)
	}
	for _, id := range f.Links.Issues {
		if !issueRe.MatchString(id) {
			bad("links.issues: %q is not HAMSP-N", id)
		}
	}
	for _, r := range f.Requirements {
		if !reqRe.MatchString(r) {
			bad("requirements: %q is not FR-xx, QS-xx or C-xx", r)
		}
	}
	for id, ac := range f.Acceptance {
		if !acRe.MatchString(id) {
			bad("acceptance: %q is not AC-n", id)
		}
		if ac.Text == "" {
			bad("acceptance %s: text is missing", id)
		}
		if ac.Level != "" && !levels[ac.Level] {
			bad("acceptance %s: level %q is unknown", id, ac.Level)
		}
		if ac.Verified.NA != "" {
			bad("acceptance %s: an acceptance criterion cannot be n/a", id)
		}
	}
	for id := range f.Checklist {
		if !itemRe.MatchString(id) {
			bad("checklist: %q is not T-01 to T-12", id)
		}
	}
	for _, id := range f.QualityScenarios.Touched {
		if !qsRe.MatchString(id) {
			bad("quality_scenarios.touched: %q is not QS-xx", id)
		}
	}
	for id := range f.QualityScenarios.Tests {
		if !qsRe.MatchString(id) {
			bad("quality_scenarios.tests: %q is not QS-xx", id)
		}
	}
	for name, d := range map[string]string{"plan_approved": f.PlanApproved, "structure_approved": f.StructureApproved} {
		if d != "" && !dateRe.MatchString(d) {
			bad("%s %q is not YYYY-MM-DD", name, d)
		}
	}
	for i, r := range f.Reviews {
		if !dateRe.MatchString(r.Date) {
			bad("reviews[%d]: date %q is not YYYY-MM-DD", i, r.Date)
		}
		if !commitRe.MatchString(r.Commit) {
			bad("reviews[%d]: commit %q is not a commit hash", i, r.Commit)
		}
		if r.Scope != "full" && r.Scope != "delta" {
			bad("reviews[%d]: scope %q must be full or delta", i, r.Scope)
		}
	}
	for i, r := range f.Runs {
		if !dateRe.MatchString(r.Date) {
			bad("runs[%d]: date %q is not YYYY-MM-DD", i, r.Date)
		}
		if !commitRe.MatchString(r.Commit) {
			bad("runs[%d]: commit %q is not a commit hash", i, r.Commit)
		}
		if _, ok := stages[r.StageChecked]; !ok {
			bad("runs[%d]: stage_checked %q is unknown", i, r.StageChecked)
		}
		if r.Verdict != "ready" && r.Verdict != "not-ready" {
			bad("runs[%d]: verdict %q must be ready or not-ready", i, r.Verdict)
		}
	}
	return errs
}

// highestReady is the highest stage a verify run reported Ready for, or 0.
func (f *Feature) highestReady() int {
	best := 0
	for _, r := range f.Runs {
		if r.Verdict == "ready" && stages[r.StageChecked] > best {
			best = stages[r.StageChecked]
		}
	}
	return best
}

// verifiedTests lists every test the file claims, with the ID it verifies.
func (f *Feature) verifiedTests() []claim {
	var out []claim
	add := func(id string, v Verified) {
		for _, t := range v.Tests {
			out = append(out, claim{id: id, test: t})
		}
	}
	for id, ac := range f.Acceptance {
		add(id, ac.Verified)
	}
	for id, it := range f.Checklist {
		add(id, it.Verified)
	}
	for id, it := range f.QualityScenarios.Tests {
		add(id, it.Verified)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].id != out[j].id {
			return out[i].id < out[j].id
		}
		return out[i].test < out[j].test
	})
	return out
}

type claim struct{ id, test string }
