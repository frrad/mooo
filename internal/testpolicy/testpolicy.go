// Package testpolicy enforces repository rules about what tests must exercise.
//
// A test file under internal/ must reference production code: a top-level
// declaration from a non-test file of its own package, or an exported symbol
// of another non-testsupport package in this module. A file that only checks
// a self-contained model against a fixture compares the model with itself and
// says nothing about mooo. See research/parity-fixtures.md.
package testpolicy

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ModulePath is the import path prefix of this repository's packages.
const ModulePath = "github.com/frrad/mooo"

// ModelOnlyTests returns the slash-separated paths, relative to root, of
// _test.go files under root/internal that reference no production code.
func ModelOnlyTests(root string) ([]string, error) {
	dirs := map[string][]string{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			dir := filepath.Dir(path)
			dirs[dir] = append(dirs[dir], path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var offenders []string
	for _, files := range dirs {
		got, err := modelOnlyInDir(files)
		if err != nil {
			return nil, err
		}
		for _, path := range got {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil, err
			}
			offenders = append(offenders, filepath.ToSlash(rel))
		}
	}
	sort.Strings(offenders)
	return offenders, nil
}

func modelOnlyInDir(files []string) ([]string, error) {
	fset := token.NewFileSet()
	production := map[string]bool{}
	var tests []*ast.File
	var testPaths []string
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("testpolicy: parse %s: %w", path, err)
		}
		if strings.HasSuffix(path, "_test.go") {
			tests = append(tests, file)
			testPaths = append(testPaths, path)
			continue
		}
		for name := range topLevelNames(file) {
			production[name] = true
		}
	}
	var offenders []string
	for i, file := range tests {
		if !referencesProduction(file, production) {
			offenders = append(offenders, testPaths[i])
		}
	}
	return offenders, nil
}

func referencesProduction(file *ast.File, production map[string]bool) bool {
	local := topLevelNames(file)
	internalTest := !strings.HasSuffix(file.Name.Name, "_test")
	moduleImports := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.HasPrefix(path, ModulePath+"/") || strings.Contains(path, "/testsupport") {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		moduleImports[name] = true
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := n.X.(*ast.Ident); ok && moduleImports[pkg.Name] {
				found = true
			}
		case *ast.Ident:
			if internalTest && production[n.Name] && !local[n.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

func topLevelNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil {
				names[decl.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					names[spec.Name.Name] = true
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						names[name.Name] = true
					}
				}
			}
		}
	}
	delete(names, "_")
	delete(names, "init")
	return names
}

// ReadAllowlist reads one repository-relative path per line, ignoring blank
// lines and lines starting with '#'.
func ReadAllowlist(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries = append(entries, line)
	}
	return entries, nil
}
