package codegen

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ActionRef is one "module.actionName" string literal passed to useAction
// or callAction in a module's frontend source.
type ActionRef struct {
	File  string
	Line  int
	Route string
}

// actionRefPattern matches a useAction or callAction call whose first
// argument is a string literal, with or without type arguments.
var actionRefPattern = regexp.MustCompile("\\b(?:useAction|callAction)\\s*(?:<[^()]*?>)?\\s*\\(\\s*(?:'([^'\\n]*)'|\"([^\"\\n]*)\"|`([^`]*)`)")

// ScanActionRefs returns every useAction/callAction string literal in the
// .ts and .tsx files under dir/frontend/src, skipping skipFile (the
// generated client, which may be stale until this run rewrites it). A
// template literal with a substitution is not a fixed name and is left out.
// A module with no frontend/src has no references.
func ScanActionRefs(dir, skipFile string) ([]ActionRef, error) {
	root := filepath.Join(dir, "frontend", "src")
	skipAbs, err := filepath.Abs(skipFile)
	if err != nil {
		return nil, fmt.Errorf("resolve output path: %w", err)
	}

	var refs []ActionRef
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && SkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !IsFrontendSourceFile(d.Name()) {
			return nil
		}
		if abs, err := filepath.Abs(path); err == nil && abs == skipAbs {
			return nil
		}
		found, err := scanFileActionRefs(path)
		if err != nil {
			return err
		}
		refs = append(refs, found...)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan frontend sources for action references: %w", err)
	}
	return refs, nil
}

func scanFileActionRefs(path string) ([]ActionRef, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var refs []ActionRef
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for line := 1; sc.Scan(); line++ {
		for _, m := range actionRefPattern.FindAllStringSubmatch(sc.Text(), -1) {
			route := m[1] + m[2] + m[3]
			if strings.Contains(route, "${") {
				continue
			}
			refs = append(refs, ActionRef{File: path, Line: line, Route: route})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return refs, nil
}
