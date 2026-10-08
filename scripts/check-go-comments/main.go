// Command check-go-comments checks Go source for tracker references and nonportable API documentation.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	trackerReference = regexp.MustCompile(`(?i)#\s*[0-9]+\b|\b(?:goerp-|issues?\s+|PR\s+|pull request\s+)[0-9]+\b|github\.com/[^\s]+/(issues|pull)/[0-9]+\b`)
	privateReference = regexp.MustCompile(`[A-Za-z0-9_-]+\.md\b|§|nexus-docs|internal/(engine|module|cli)/`)
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	paths, err := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "*.go").Output()
	if err != nil {
		return fmt.Errorf("list Go sources: %w", err)
	}

	failed := false

	for path := range strings.SplitSeq(string(paths), "\x00") {
		if path == "" || isTestdata(path) {
			continue
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		findings, err := checkComments(path, src)
		if err != nil {
			return err
		}

		for _, finding := range findings {
			fmt.Fprintln(out, finding)
			failed = true
		}
	}

	if failed {
		return fmt.Errorf("comments must explain behavior without tracker references; SDK and ABI comments must also avoid private documentation and engine paths")
	}

	return nil
}

func isTestdata(path string) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if part == "testdata" {
			return true
		}
	}

	return false
}

func checkComments(path string, src []byte) ([]string, error) {
	if isTestdata(path) {
		return nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	portableAPI := strings.HasPrefix(filepath.ToSlash(path), "sdk/go/") || strings.HasPrefix(filepath.ToSlash(path), "contract/abi/v1/")
	var findings []string

	for _, group := range file.Comments {
		var parts []string

		for _, entry := range group.List {
			text, line := strings.CutPrefix(entry.Text, "//")
			if !line {
				text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
			}

			parts = append(parts, text)
		}

		comment := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
		position := fset.Position(group.Pos())

		if trackerReference.MatchString(comment) {
			findings = append(findings, fmt.Sprintf("%s: tracker reference in Go comment", position))
		}

		if portableAPI && privateReference.MatchString(comment) {
			findings = append(findings, fmt.Sprintf("%s: SDK and ABI comments must be self-contained", position))
		}
	}

	return findings, nil
}
