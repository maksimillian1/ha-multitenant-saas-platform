package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Repo answers questions about the repository the features live in.
type Repo interface {
	// Files lists the files in the working tree: tracked and untracked,
	// without ignored ones.
	Files() ([]string, error)
	// ChangedSince lists the files that differ between commit and the
	// working tree, including untracked files.
	ChangedSince(commit string) ([]string, error)
	// ChangedInPR lists the files a pull request changes: the diff from the
	// merge base with base to HEAD.
	ChangedInPR(base string) ([]string, error)
	// Read returns the content of a file, by its path from the root.
	Read(path string) ([]byte, error)
}

type gitRepo struct{ root string }

func (g gitRepo) git(args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.root
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func (g gitRepo) Files() ([]string, error) {
	return g.git("ls-files", "--cached", "--others", "--exclude-standard")
}

func (g gitRepo) ChangedSince(commit string) ([]string, error) {
	changed, err := g.git("diff", "--name-only", commit, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := g.git("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return append(changed, untracked...), nil
}

func (g gitRepo) ChangedInPR(base string) ([]string, error) {
	return g.git("diff", "--name-only", base+"...HEAD", "--")
}

func (g gitRepo) Read(path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(g.root, filepath.FromSlash(path)))
}

// globRe turns a glob from a feature file into a regexp: "**" matches any
// number of path segments, "*" and "?" stay inside one segment.
func globRe(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func matchAny(globs []string, files []string) ([]string, error) {
	var res []*regexp.Regexp
	for _, g := range globs {
		re, err := globRe(g)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", g, err)
		}
		res = append(res, re)
	}
	var out []string
	for _, f := range files {
		for _, re := range res {
			if re.MatchString(f) {
				out = append(out, f)
				break
			}
		}
	}
	return out, nil
}

// checkRepo checks what a feature file says about the repository: its code
// globs match files, and every verified test exists and says what it verifies.
func checkRepo(f *Feature, repo Repo, files []string) []error {
	var errs []error
	bad := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", f.file, fmt.Sprintf(format, a...)))
	}
	if f.Stage != "" {
		for _, g := range f.Code {
			m, err := matchAny([]string{g}, files)
			if err != nil {
				bad("code: %v", err)
				continue
			}
			if len(m) == 0 {
				bad("code: %q matches no file", g)
			}
		}
	}
	for _, c := range f.verifiedTests() {
		if err := checkTest(repo, f.Feature, c); err != nil {
			bad("%s: %v", c.id, err)
		}
	}
	return errs
}

// checkTest finds "path:TestName" and the "Verifies:" comment above it,
// which must name the feature and the ID.
func checkTest(repo Repo, slug string, c claim) error {
	path, name, ok := strings.Cut(c.test, ":")
	if !ok || path == "" || name == "" {
		return fmt.Errorf("test %q must be path:TestName", c.test)
	}
	data, err := repo.Read(path)
	if err != nil {
		return fmt.Errorf("test %q: file not found", c.test)
	}
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	decl := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	if strings.HasSuffix(path, ".go") {
		decl = regexp.MustCompile(`^func (\([^)]*\) )?` + regexp.QuoteMeta(name) + `\(`)
	}
	for i, l := range lines {
		if !decl.MatchString(l) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			t := strings.TrimSpace(lines[j])
			if !(strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*")) {
				break
			}
			if verifies(t, slug, c.id) {
				return nil
			}
		}
		return fmt.Errorf("test %q has no \"Verifies: %s ... %s\" comment above it", c.test, slug, c.id)
	}
	return fmt.Errorf("test %q: %s not found in %s", c.test, name, path)
}

// verifies reports whether a comment line such as
// "// Verifies: tenant-suspend AC-2; T-01" names the feature and the ID.
func verifies(line, slug, id string) bool {
	_, rest, ok := strings.Cut(line, "Verifies:")
	if !ok {
		return false
	}
	fields := strings.FieldsFunc(rest, func(r rune) bool { return r == ' ' || r == ',' || r == ';' })
	var hasSlug, hasID bool
	for _, f := range fields {
		hasSlug = hasSlug || f == slug
		hasID = hasID || f == id
	}
	return hasSlug && hasID
}
