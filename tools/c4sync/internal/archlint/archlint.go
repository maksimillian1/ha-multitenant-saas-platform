// Package archlint runs the go-arch-lint binary: the mapping of packages to
// components, and the check of imports against the rules. go-arch-lint has no
// public Go API, so this is a thin adapter over its JSON output.
package archlint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/maksimillian1/ha-multitenant-saas-platform/tools/c4sync/internal/model"
)

const notAttached = "[not attached]"

// Runner calls one go-arch-lint binary.
type Runner struct {
	Bin string // path or name in PATH
}

func (r Runner) run(args ...string) ([]byte, int, error) {
	cmd := exec.Command(r.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		return nil, 0, fmt.Errorf("go-arch-lint: %w", err)
	}
	if stdout.Len() == 0 {
		return nil, code, fmt.Errorf("go-arch-lint %s: exit %d: %s", args[0], code, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), code, nil
}

type envelope struct {
	Type    string
	Payload json.RawMessage
}

// errorPayload is what go-arch-lint prints with --json when it fails, for
// example on an invalid config.
type errorPayload struct {
	Error string
}

func decode(out []byte, want string, v any) error {
	var e envelope
	if err := json.Unmarshal(out, &e); err != nil {
		// On some failures (for example a missing config) it prints plain text.
		return fmt.Errorf("go-arch-lint: %s", strings.TrimSpace(string(out)))
	}
	if e.Type != want {
		var p errorPayload
		_ = json.Unmarshal(e.Payload, &p)
		if p.Error != "" {
			return fmt.Errorf("go-arch-lint: %s", p.Error)
		}
		return fmt.Errorf("go-arch-lint: unexpected output type %q: %s", e.Type, bytes.TrimSpace(e.Payload))
	}
	return json.Unmarshal(e.Payload, v)
}

type mappingPayload struct {
	ProjectDirectory string
	ModuleName       string
	MappingGrouped   []struct {
		ComponentName string
		FileNames     []string
	}
}

// Mapping runs `go-arch-lint mapping --json` and converts files to packages.
func (r Runner) Mapping(projectPath, archFile string) (model.Mapping, error) {
	out, _, err := r.run("mapping", "--json", "--scheme", "grouped",
		"--project-path", projectPath, "--arch-file", archFile)
	if err != nil {
		return model.Mapping{}, err
	}
	var p mappingPayload
	if err := decode(out, "models.Mapping", &p); err != nil {
		return model.Mapping{}, err
	}
	m := model.Mapping{Module: p.ModuleName, Components: map[string][]string{}}
	seen := map[string]bool{}
	for _, g := range p.MappingGrouped {
		for _, f := range g.FileNames {
			pkg, err := packageOf(p.ProjectDirectory, p.ModuleName, f)
			if err != nil {
				return model.Mapping{}, err
			}
			key := g.ComponentName + "\x00" + pkg
			if seen[key] {
				continue
			}
			seen[key] = true
			if strings.EqualFold(g.ComponentName, notAttached) {
				m.Unmapped = append(m.Unmapped, pkg)
			} else {
				m.Components[g.ComponentName] = append(m.Components[g.ComponentName], pkg)
			}
		}
	}
	m.Normalize()
	return m, nil
}

func packageOf(projectDir, module, file string) (string, error) {
	rel, err := filepath.Rel(projectDir, filepath.Dir(file))
	if err != nil {
		return "", err
	}
	if rel == "." {
		return module, nil
	}
	return path.Join(module, filepath.ToSlash(rel)), nil
}

type reference struct {
	Valid bool
	File  string
	Line  int
}

type checkPayload struct {
	ExecutionWarnings []struct {
		Text string
		File string
		Line int
	}
	ArchWarningsDeps []struct {
		ComponentName      string
		FileAbsolutePath   string
		ResolvedImportName string
		Reference          reference
	}
	ArchWarningsDeepScan []struct {
		Gate struct {
			ComponentName string
			MethodName    string
			Definition    reference
		}
		Dependency struct {
			ComponentName string
			Name          string
			InjectionAST  string
			Injection     reference
		}
	}
	OmittedCount int
}

// Violation is one broken rule reported by go-arch-lint.
type Violation struct {
	Pos     model.Pos // file relative to the project
	Message string
}

// Check runs `go-arch-lint check --json`. Files that match no component are
// not returned: the mapping already lists them as unmapped packages.
func (r Runner) Check(projectPath, archFile string) ([]Violation, error) {
	out, _, err := r.run("check", "--json", "--max-warnings", "1000",
		"--project-path", projectPath, "--arch-file", archFile)
	if err != nil {
		return nil, err
	}
	var p checkPayload
	if err := decode(out, "models.Check", &p); err != nil {
		return nil, err
	}
	rel := func(f string) string {
		if r, err := filepath.Rel(projectPath, f); err == nil {
			return filepath.ToSlash(r)
		}
		return f
	}
	var vs []Violation
	for _, w := range p.ExecutionWarnings {
		vs = append(vs, Violation{Pos: model.Pos{File: rel(w.File), Line: w.Line}, Message: w.Text})
	}
	for _, w := range p.ArchWarningsDeps {
		file := w.FileAbsolutePath
		if w.Reference.Valid {
			file = w.Reference.File
		}
		vs = append(vs, Violation{
			Pos:     model.Pos{File: rel(file), Line: w.Reference.Line},
			Message: fmt.Sprintf("component %s must not import %s", w.ComponentName, w.ResolvedImportName),
		})
	}
	for _, w := range p.ArchWarningsDeepScan {
		vs = append(vs, Violation{
			Pos: model.Pos{File: rel(w.Dependency.Injection.File), Line: w.Dependency.Injection.Line},
			Message: fmt.Sprintf("component %s gets %s from component %s in %s (deepScan)",
				w.Gate.ComponentName, w.Dependency.Name, w.Dependency.ComponentName, w.Gate.MethodName),
		})
	}
	if p.OmittedCount > 0 {
		vs = append(vs, Violation{Message: fmt.Sprintf("%d more warnings omitted by go-arch-lint", p.OmittedCount)})
	}
	return vs, nil
}
