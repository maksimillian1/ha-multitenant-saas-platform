package main

import (
	"fmt"
	"strings"
)

const (
	startMarker = "<!-- features:start -->"
	endMarker   = "<!-- features:end -->"
)

// renderTable builds the map from the feature files alone. It does not look
// at git, so the map changes only when a feature file changes.
func renderTable(features []*Feature) string {
	var b strings.Builder
	b.WriteString("| Feature | Title | Stage | Last verify | Gaps | Last review | Issues |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, f := range features {
		name := fmt.Sprintf("[`%s`](%s.yaml)", f.Feature, f.Feature)
		if f.Critical {
			name += " **critical**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			name, cell(f.Title), orDash(f.Stage), lastVerify(f), cell(gaps(f)), lastReview(f), strings.Join(f.Links.Issues, ", "))
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

func lastReview(f *Feature) string {
	if len(f.Reviews) == 0 {
		return "—"
	}
	r := f.Reviews[len(f.Reviews)-1]
	return fmt.Sprintf("%s at %s", r.Date, short(r.Commit))
}

// gaps are the failed and not-checked items of the latest verify run.
func gaps(f *Feature) string {
	if len(f.Runs) == 0 {
		return "not verified"
	}
	last := f.Runs[len(f.Runs)-1]
	var g []string
	if len(last.Failed) > 0 {
		g = append(g, "failed: "+strings.Join(last.Failed, ", "))
	}
	if len(last.NotChecked) > 0 {
		g = append(g, "not checked: "+strings.Join(last.NotChecked, ", "))
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
