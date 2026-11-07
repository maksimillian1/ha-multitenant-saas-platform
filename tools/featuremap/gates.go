package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Gate is one row of the gates table in a feature file: the state of a
// gate from docs/process/feature-stages.md, "Gates per stage".
type Gate struct {
	Status string `yaml:"status"` // pass | fail | n/a | not-checked
	By     string `yaml:"by"`     // verify | owner
	Date   string `yaml:"date"`
	Commit string `yaml:"commit"` // the commit checked or reviewed up to
	Level  string `yaml:"level"`  // G-04 only: basic | full
	Note   string `yaml:"note"`
}

var gateIDs = []string{"G-01", "G-02", "G-03", "G-04", "G-05", "G-06", "G-07", "G-08"}

var gateRe = regexp.MustCompile(`^G-0[1-8]$`)

// required lists the gates a stage needs, from the table in
// docs/process/feature-stages.md. G-05 is informational and never required.
// G-04 needs the level in brackets.
var required = map[string][]string{
	"proto":  {"G-01"},
	"alpha":  {"G-01", "G-02", "G-03", "G-04:basic", "G-07"},
	"beta":   {"G-01", "G-02", "G-03", "G-04:full", "G-07", "G-08"},
	"stable": {"G-02", "G-03", "G-04:full", "G-06", "G-07", "G-08"},
}

// passes reports whether a gate counts as met: pass, or n/a with a reason.
// level is the G-04 level the stage needs, or "".
func (g Gate) passes(level string) bool {
	switch g.Status {
	case "n/a":
		return true
	case "pass":
		return level == "" || g.Level == "full" || g.Level == level
	}
	return false
}

// missingGates lists the gates the feature's stage needs but does not meet.
func (f *Feature) missingGates() []string {
	var out []string
	for _, req := range required[f.Stage] {
		id, level, _ := strings.Cut(req, ":")
		g, ok := f.Gates[id]
		switch {
		case !ok:
			out = append(out, id+" not recorded")
		case !g.passes(level):
			if g.Status == "pass" {
				out = append(out, fmt.Sprintf("%s is %s, needs %s", id, g.Level, level))
			} else {
				out = append(out, id+" is "+g.Status)
			}
		}
	}
	return out
}

func (f *Feature) validateGates(bad func(string, ...any)) {
	ids := make([]string, 0, len(f.Gates))
	for id := range f.Gates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g := f.Gates[id]
		if !gateRe.MatchString(id) {
			bad("gates: %q is not G-01 to G-08", id)
			continue
		}
		switch g.Status {
		case "pass", "fail", "n/a", "not-checked":
		default:
			bad("gates %s: status %q must be pass, fail, n/a or not-checked", id, g.Status)
		}
		if g.By != "verify" && g.By != "owner" {
			bad("gates %s: by %q must be verify or owner", id, g.By)
		}
		if !dateRe.MatchString(g.Date) {
			bad("gates %s: date %q is not YYYY-MM-DD", id, g.Date)
		}
		if g.Commit != "" && !commitRe.MatchString(g.Commit) {
			bad("gates %s: commit %q is not a commit hash", id, g.Commit)
		}
		if g.Status == "n/a" && g.Note == "" {
			bad("gates %s: n/a needs a note with the reason", id)
		}
		if id == "G-04" && g.Status == "pass" && g.Level != "basic" && g.Level != "full" {
			bad("gates G-04: a pass needs level basic or full")
		}
		if id != "G-04" && g.Level != "" {
			bad("gates %s: level is only for G-04", id)
		}
		if id == "G-06" && g.Status == "pass" && g.Commit == "" {
			bad("gates G-06: a review needs the commit it was read up to")
		}
	}
	if f.Stage != "" {
		if miss := f.missingGates(); len(miss) > 0 {
			bad("stage %s is not backed by its gates: %s", f.Stage, strings.Join(miss, "; "))
		}
	}
}

// gateCell is how one gate shows in the map.
func gateCell(f *Feature, id string) string {
	g, ok := f.Gates[id]
	if !ok {
		return ""
	}
	s := map[string]string{"pass": "✓", "fail": "✗", "n/a": "n/a", "not-checked": "?"}[g.Status]
	if g.Level != "" {
		s += " " + g.Level
	}
	if g.By == "owner" {
		s += " (owner)"
	}
	return s
}
