// Package config reads c4sync.yml: C4 metadata and settings that the
// go-arch-lint config has no room for.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the content of c4sync.yml. Paths are relative to the file.
type Config struct {
	// Name of the service, used as the title of the generated code workspace.
	Name string `yaml:"name"`
	// ProjectPath is the Go module root. Default ".".
	ProjectPath string `yaml:"projectPath"`
	// ArchFile is the go-arch-lint config. Default ".go-arch-lint.yml" in ProjectPath.
	ArchFile string `yaml:"archFile"`
	// IdentifierPrefix starts every generated DSL identifier, so fragments of
	// several services can live in one workspace. Default "c".
	IdentifierPrefix string `yaml:"identifierPrefix"`
	Output           Output `yaml:"output"`
	// Containers split a module that builds several binaries: each gets a C3
	// fragment with only the components its main package imports.
	Containers []Container          `yaml:"containers"`
	Code       Code                 `yaml:"code"`
	Components map[string]Component `yaml:"components"`
	Externals  []External           `yaml:"externals"`
	Detectors  Detectors            `yaml:"detectors"`
	// Important components are listed first by diff.
	Important []string `yaml:"important"`

	dir           string
	componentLine map[string]int
	file          string
}

// Output says where generate writes files. An empty path skips that output.
type Output struct {
	Components string `yaml:"components"` // C3 fragment to !include inside a container
	Code       string `yaml:"code"`       // standalone code-level workspace
}

// Container is one binary of the module, drawn as one C4 container.
type Container struct {
	Name string `yaml:"name"` // used in the fragment header and the default prefix
	// Main is the main package, relative to the module root, e.g. "cmd/writer".
	Main string `yaml:"main"`
	// Output is the C3 fragment of this container.
	Output string `yaml:"output"`
	// IdentifierPrefix defaults to "<identifierPrefix>_<name in lower case>".
	IdentifierPrefix string `yaml:"identifierPrefix"`
}

// Code limits the code-level views.
type Code struct {
	MaxElements int `yaml:"maxElements"` // per component view; default 30
	MaxMethods  int `yaml:"maxMethods"`  // per element; default 6
}

// Component is C4 metadata for one go-arch-lint component.
type Component struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Technology  string   `yaml:"technology"`
	Tags        []string `yaml:"tags"`
}

// External maps imports of a library to an element of the workspace.
type External struct {
	Import      string `yaml:"import"`      // import path prefix
	Element     string `yaml:"element"`     // workspace identifier
	Description string `yaml:"description"` // default "Uses"
	Technology  string `yaml:"technology"`  // default: the import path prefix
}

// Detectors switches on the detectors of external interfaces.
type Detectors struct {
	HTTP   bool    `yaml:"http"`
	Topics *Topics `yaml:"topics"`
	SQL    *SQL    `yaml:"sql"`
}

// Topics finds message topics in calls and struct literals.
type Topics struct {
	Element string    `yaml:"element"`
	Produce []Matcher `yaml:"produce"`
	Consume []Matcher `yaml:"consume"`
}

// SQL finds table names in queries and migrations.
type SQL struct {
	Element    string    `yaml:"element"`
	Calls      []Matcher `yaml:"calls"`
	Migrations []string  `yaml:"migrations"` // folders with .sql files, relative to ProjectPath
	// Ignore lists table and function names to skip, as path.Match patterns.
	// pg_*, information_schema.* and common built-in functions are always skipped.
	Ignore []string `yaml:"ignore"`
}

// Matcher selects a value in the code.
//
// Call is "import/path.Func" or "import/path.Type.Method"; the part after the
// package path may use path.Match patterns, e.g. "github.com/jackc/pgx/v5.*.Exec".
// Arg is the index of the argument that holds the value; -1 means every
// argument from the first variadic one.
//
// Field is "import/path.Type.Field": the value of that field in a composite literal.
type Matcher struct {
	Call  string `yaml:"call"`
	Arg   int    `yaml:"arg"`
	Field string `yaml:"field"`
}

// Load reads and validates the file.
func Load(file string) (*Config, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: the file is empty", file)
	} else if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	c.componentLine = componentLines(&root)
	c.file = file
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	c.dir = filepath.Dir(abs)
	c.defaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) defaults() {
	if c.ProjectPath == "" {
		c.ProjectPath = "."
	}
	if c.ArchFile == "" {
		c.ArchFile = filepath.Join(c.ProjectPath, ".go-arch-lint.yml")
	}
	if c.IdentifierPrefix == "" {
		c.IdentifierPrefix = "c"
	}
	if c.Code.MaxElements == 0 {
		c.Code.MaxElements = 30
	}
	if c.Code.MaxMethods == 0 {
		c.Code.MaxMethods = 6
	}
	if c.Name == "" {
		c.Name = filepath.Base(c.Project())
	}
}

func (c *Config) validate() error {
	var errs []string
	for i, e := range c.Externals {
		if e.Import == "" || e.Element == "" {
			errs = append(errs, fmt.Sprintf("externals[%d]: import and element are required", i))
		}
	}
	check := func(where string, ms []Matcher) {
		for i, m := range ms {
			if (m.Call == "") == (m.Field == "") {
				errs = append(errs, fmt.Sprintf("%s[%d]: set exactly one of call and field", where, i))
			}
			if m.Call != "" {
				if _, _, err := SplitSymbol(m.Call); err != nil {
					errs = append(errs, fmt.Sprintf("%s[%d]: %v", where, i, err))
				}
			}
			if m.Field != "" {
				if _, rest, err := SplitSymbol(m.Field); err != nil || len(strings.Split(rest, ".")) != 2 {
					errs = append(errs, fmt.Sprintf("%s[%d]: field must be import/path.Type.Field", where, i))
				}
			}
		}
	}
	seen := map[string]bool{}
	for i, ct := range c.Containers {
		if ct.Name == "" || ct.Main == "" || ct.Output == "" {
			errs = append(errs, fmt.Sprintf("containers[%d]: name, main and output are required", i))
		}
		if seen[ct.Name] {
			errs = append(errs, fmt.Sprintf("containers[%d]: duplicate name %q", i, ct.Name))
		}
		seen[ct.Name] = true
	}
	if t := c.Detectors.Topics; t != nil {
		check("detectors.topics.produce", t.Produce)
		check("detectors.topics.consume", t.Consume)
	}
	if s := c.Detectors.SQL; s != nil {
		check("detectors.sql.calls", s.Calls)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", c.file, strings.Join(errs, "; "))
	}
	return nil
}

// CheckComponents fails when the file has metadata for a component that the
// go-arch-lint config does not define.
func (c *Config) CheckComponents(known map[string]bool) error {
	var unknown []string
	for id := range c.Components {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	for _, id := range c.Important {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	var msgs []string
	for _, id := range unknown {
		if l := c.componentLine[id]; l > 0 {
			msgs = append(msgs, fmt.Sprintf("%s:%d: component %q is not defined in %s", c.file, l, id, c.ArchFile))
		} else {
			msgs = append(msgs, fmt.Sprintf("%s: component %q is not defined in %s", c.file, id, c.ArchFile))
		}
	}
	return fmt.Errorf("%s", strings.Join(msgs, "\n"))
}

// Path resolves a path from the file against the file's folder.
func (c *Config) Path(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

// Project is the absolute module root.
func (c *Config) Project() string { return c.Path(c.ProjectPath) }

// Arch is the absolute path of the go-arch-lint config.
func (c *Config) Arch() string { return c.Path(c.ArchFile) }

// File is the path the config was loaded from.
func (c *Config) File() string { return c.file }

func componentLines(root *yaml.Node) map[string]int {
	lines := map[string]int{}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return lines
	}
	top := root.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "components" && top.Content[i].Value != "important" {
			continue
		}
		v := top.Content[i+1]
		switch v.Kind {
		case yaml.MappingNode:
			for j := 0; j+1 < len(v.Content); j += 2 {
				lines[v.Content[j].Value] = v.Content[j].Line
			}
		case yaml.SequenceNode:
			for _, n := range v.Content {
				if _, ok := lines[n.Value]; !ok {
					lines[n.Value] = n.Line
				}
			}
		}
	}
	return lines
}

// SplitSymbol splits "import/path.Rest.Of.It" into the package path and the rest.
// The package path ends at the first dot after the last slash.
func SplitSymbol(s string) (pkg, rest string, err error) {
	slash := strings.LastIndex(s, "/")
	dot := strings.Index(s[slash+1:], ".")
	if dot < 0 {
		return "", "", fmt.Errorf("%q: expected import/path.Name", s)
	}
	pkg, rest = s[:slash+1+dot], s[slash+1+dot+1:]
	if pkg == "" || rest == "" {
		return "", "", fmt.Errorf("%q: expected import/path.Name", s)
	}
	for _, part := range strings.Split(rest, ".") {
		if _, err := path.Match(part, ""); err != nil {
			return "", "", fmt.Errorf("%q: bad pattern %q", s, part)
		}
	}
	return pkg, rest, nil
}

// ArchComponents returns the component names declared in the go-arch-lint
// config. The go-arch-lint mapping lists only components that have files, so
// a component declared there before its package exists would look unknown.
func ArchComponents(archFile string) (map[string]bool, error) {
	raw, err := os.ReadFile(archFile)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Components map[string]yaml.Node `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", archFile, err)
	}
	out := map[string]bool{}
	for name := range doc.Components {
		out[name] = true
	}
	return out, nil
}
