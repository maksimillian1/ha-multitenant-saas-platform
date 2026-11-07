// Command featuremap validates the feature files in docs/features, writes
// the map of all features into docs/features/README.md, and lists the
// features a pull request touches.
//
//	go -C tools/featuremap run .                     # validate and write the map
//	go -C tools/featuremap run . -check              # validate; fail if the map is out of date
//	go -C tools/featuremap run . -pr origin/master   # print the pull request report
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
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
	pr := flag.String("pr", "", "print the features touched by the changes since this base ref, and exit")
	root := flag.String("root", "", "repository root (default: git rev-parse --show-toplevel)")
	flag.Parse()

	if *root == "" {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			fail(fmt.Errorf("find the repository root: %w", err))
		}
		*root = strings.TrimSpace(string(out))
	}
	repo := gitRepo{root: *root}
	var err error
	if *pr != "" {
		err = report(*root, repo, *pr, os.Stdout)
	} else {
		err = run(*root, repo, *check)
	}
	if err != nil {
		fail(err)
	}
}

// run validates every feature file and writes the map, or with check only
// compares it.
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

	path := filepath.Join(root, readme)
	doc, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := replaceBlock(string(doc), renderTable(features))
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

func report(root string, repo Repo, base string, w io.Writer) error {
	features, err := loadFeatures(root, featuresDir)
	if err != nil {
		return err
	}
	out, err := prReport(features, repo, base)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, out)
	return err
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "featuremap:", err)
	os.Exit(1)
}
