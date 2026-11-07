// Package textdiff prints a unified diff of two texts, line by line.
package textdiff

import (
	"fmt"
	"strings"
)

// Unified returns a unified diff with n lines of context, or "" when equal.
func Unified(oldName, newName, a, b string, n int) string {
	if a == b {
		return ""
	}
	x, y := lines(a), lines(b)
	ops := edits(x, y)
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(0, i-n)
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*n || run == len(ops) {
				end = min(len(ops), end+n)
				break
			}
			end = run
		}
		ai, bi := ops[start].ai, ops[start].bi
		ac, bc := 0, 0
		for _, o := range ops[start:end] {
			if o.kind != '+' {
				ac++
			}
			if o.kind != '-' {
				bc++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", ai+1, ac, bi+1, bc)
		for _, o := range ops[start:end] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		i = end
	}
	return out.String()
}

type op struct {
	kind   byte
	text   string
	ai, bi int
}

func lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// edits computes a shortest edit script with a longest common subsequence table.
// Generated files are small, so O(n*m) is fine.
func edits(x, y []string) []op {
	n, m := len(x), len(y)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			ops = append(ops, op{' ', x[i], i, j})
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', x[i], i, j})
			i++
		default:
			ops = append(ops, op{'+', y[j], i, j})
			j++
		}
	}
	return ops
}
