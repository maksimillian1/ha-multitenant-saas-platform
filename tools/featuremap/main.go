// Command featuremap validates the feature files in docs/features and
// writes the map of all features into docs/features/README.md.
//
//	go -C tools/featuremap run .          # write the map
//	go -C tools/featuremap run . -check   # fail if the map is out of date
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	featuresDir = "docs/features"
	readme      = "docs/features/README.md"
)

func main() {
	check := flag.Bool("check", false, "fail when the map is out of date instead of writing it")
	root := flag.String("root", "", "repository root (default: git rev-parse --show-toplevel)")
	flag.Parse()

	if *root == "" {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			fail(fmt.Errorf("find the repository root: %w", err))
		}
		*root = strings.TrimSpace(string(out))
	}
	if err := run(*root, gitRepo{root: *root}, *check); err != nil {
		fail(err)
	}
}

func run(root string, repo Repo, check bool) error {
	features, err := loadFeatures(root, featuresDir)
	if err != nil {
		return err
	}
	files, err := repo.Files()
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range features {
		errs = append(errs, f.validate()...)
		errs = append(errs, checkRepo(f, repo, files)...)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	rows, err := buildRows(features, repo)
	if err != nil {
		return err
	}
	path := filepath.Join(root, readme)
	doc, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := replaceBlock(string(doc), renderTable(rows))
	if err != nil {
		return fmt.Errorf("%s: %w", readme, err)
	}
	if updated == string(doc) {
		return nil
	}
	if check {
		return fmt.Errorf("%s is out of date: run `go -C tools/featuremap run .` and commit it", readme)
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "featuremap:", err)
	os.Exit(1)
}
