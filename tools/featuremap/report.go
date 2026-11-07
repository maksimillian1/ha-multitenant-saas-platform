package main

import (
	"fmt"
	"strings"
)

// prMarker lets CI find and update its own comment on the pull request.
const prMarker = "<!-- featuremap-pr -->"

// prReport lists the features whose code a pull request changes, for the
// owner to decide what the change means for each of them. It changes nothing.
func prReport(features []*Feature, repo Repo, base string) (string, error) {
	prFiles, err := repo.ChangedInPR(base)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(prMarker + "\n### Features touched by this pull request\n\n")

	type touched struct {
		f     *Feature
		files []string
		since string
	}
	var rows []touched
	for _, f := range features {
		files, err := matchAny(f.Code, prFiles)
		if err != nil {
			return "", fmt.Errorf("%s: %w", f.file, err)
		}
		if len(files) == 0 {
			continue
		}
		t := touched{f: f, files: files, since: "—"}
		if _, commit := lastReview(f); commit != "" {
			changed, err := repo.ChangedSince(commit)
			if err != nil {
				return "", fmt.Errorf("%s: %w", f.file, err)
			}
			m, err := matchAny(f.Code, changed)
			if err != nil {
				return "", fmt.Errorf("%s: %w", f.file, err)
			}
			t.since = fmt.Sprintf("%d", len(m))
		}
		rows = append(rows, t)
	}

	if len(rows) == 0 {
		b.WriteString("No code of a feature in `docs/features/` changed.\n")
		return b.String(), nil
	}

	b.WriteString("| Feature | Stage | Critical | Files in this PR | Last review | Files changed since that review |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, t := range rows {
		review, _ := lastReview(t.f)
		crit := ""
		if t.f.Critical {
			crit = "yes"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %d | %s | %s |\n",
			t.f.Feature, orDash(t.f.Stage), crit, len(t.files), review, t.since)
	}
	b.WriteString("\nNo stage is changed automatically. For a reviewed feature, decide: read the change and move gate G-06 to this commit, or lower its `stage` by hand.\n")
	for _, t := range rows {
		fmt.Fprintf(&b, "\n<details><summary><code>%s</code>: %d files</summary>\n\n", t.f.Feature, len(t.files))
		for _, p := range t.files {
			fmt.Fprintf(&b, "* `%s`\n", p)
		}
		b.WriteString("\n</details>\n")
	}
	return b.String(), nil
}
