package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	startMarker = "<!-- features:start -->"
	endMarker   = "<!-- features:end -->"
)

// row is one line of the map, computed from a feature file and the repo.
type row struct {
	f       *Feature
	stage   string // shown stage, after going down
	changed int    // files of the feature changed since its last review
}

func buildRows(features []*Feature, repo Repo) ([]row, error) {
	var rows []row
	for _, f := range features {
		r := row{f: f, stage: f.Stage}
		if n := len(f.Reviews); n > 0 && len(f.Code) > 0 {
			changed, err := repo.ChangedSince(f.Reviews[n-1].Commit)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.file, err)
			}
			m, err := matchAny(f.Code, changed)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.file, err)
			}
			r.changed = len(m)
		}
		if f.Stage == "stable" && r.changed > 0 {
			r.stage = "beta"
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func renderTable(rows []row) string {
	var b strings.Builder
	b.WriteString("| Feature | Title | Stage | Target | Last verify | Gaps | Reviewed | Issues |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		f := r.f
		name := fmt.Sprintf("[`%s`](%s.yaml)", f.Feature, f.Feature)
		if f.Critical {
			name += " **critical**"
		}
		stage := orDash(r.stage)
		if r.stage != f.Stage {
			stage = fmt.Sprintf("%s (stable until %s)", r.stage, short(f.Reviews[len(f.Reviews)-1].Commit))
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			name, cell(f.Title), stage, f.Target, lastVerify(f), cell(gaps(f)), reviewed(r), strings.Join(f.Links.Issues, ", "))
	}
	return b.String()
}

func lastVerify(f *Feature) string {
	if len(f.Runs) == 0 {
		return "—"
	}
	r := f.Runs[len(f.Runs)-1]
	return fmt.Sprintf("%s %s for %s", r.Date, r.Verdict, r.StageChecked)
}

func reviewed(r row) string {
	if len(r.f.Reviews) == 0 {
		return "—"
	}
	last := r.f.Reviews[len(r.f.Reviews)-1]
	s := fmt.Sprintf("%s at %s", last.Date, short(last.Commit))
	if r.changed > 0 {
		s += fmt.Sprintf(", %d files changed since", r.changed)
	}
	return s
}

// gaps lists what stands between the feature and its target, from the
// file alone.
func gaps(f *Feature) string {
	var g []string
	t := stages[f.Target]
	if t >= stages["alpha"] {
		if len(f.Acceptance) == 0 {
			g = append(g, "no acceptance criteria")
		}
		var unplanned []string
		for id, ac := range f.Acceptance {
			if ac.Planned == "" {
				unplanned = append(unplanned, id)
			}
		}
		if len(unplanned) > 0 {
			sort.Strings(unplanned)
			g = append(g, "no planned test: "+strings.Join(unplanned, ", "))
		}
		if f.PlanApproved == "" {
			g = append(g, "plan not approved")
		}
	}
	if t >= stages["beta"] && f.StructureApproved == "" {
		g = append(g, "structure not approved")
	}
	if t == stages["stable"] && len(f.Reviews) == 0 {
		g = append(g, "not reviewed")
	}
	if len(f.Runs) == 0 {
		g = append(g, "not verified")
	} else {
		last := f.Runs[len(f.Runs)-1]
		if len(last.Failed) > 0 {
			g = append(g, "failed: "+strings.Join(last.Failed, ", "))
		}
		if len(last.NotChecked) > 0 {
			g = append(g, "not checked: "+strings.Join(last.NotChecked, ", "))
		}
	}
	return strings.Join(g, "; ")
}

// replaceBlock puts the table between the markers of the README.
func replaceBlock(doc, table string) (string, error) {
	start := strings.Index(doc, startMarker)
	end := strings.Index(doc, endMarker)
	if start < 0 || end < start {
		return "", fmt.Errorf("markers %s and %s not found", startMarker, endMarker)
	}
	return doc[:start+len(startMarker)] + "\n" + table + doc[end:], nil
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func cell(s string) string {
	return orDash(strings.ReplaceAll(s, "|", "\\|"))
}
